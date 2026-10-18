package requestlog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func rowAt(at time.Time, session string) row {
	return row{atMs: at.UnixMilli(), requestID: [16]byte{1, 2, 3}, traceID: "tr", sessionID: session, keyHash: "abc", authID: "auth", provider: "claude", model: "opus", agent: "claude-cli", status: 200, read: 5, verdict: Verdict{Kind: VerdictHit, Expected: 5}}
}

func seqsOf(g *ring, from, to uint64, asc bool) []uint64 {
	var out []uint64
	g.scan(from, to, asc, func(r *row) bool {
		out = append(out, r.seq)
		return true
	})
	return out
}

func TestRingAppendWrapsAndKeepsSeqContiguous(t *testing.T) {
	g := newRing(3)
	if oldest, newest, epoch := g.bounds(); oldest != 0 || newest != 0 || epoch == "" {
		t.Fatalf("empty bounds = %d %d %q", oldest, newest, epoch)
	}
	for i := 0; i < 5; i++ {
		if seq := g.append(rowAt(t0.Add(time.Duration(i)*time.Second), "s")); seq != uint64(i+1) {
			t.Fatalf("seq %d = %d", i, seq)
		}
	}
	if oldest, newest, _ := g.bounds(); oldest != 3 || newest != 5 {
		t.Fatalf("bounds after wrap = %d %d", oldest, newest)
	}
	if _, ok := g.at(2); ok {
		t.Fatal("overwritten row must be gone")
	}
	if r, ok := g.at(4); !ok || r.seq != 4 || r.atMs != t0.Add(3*time.Second).UnixMilli() {
		t.Fatalf("at(4) = %+v %v", r, ok)
	}
	if got := seqsOf(g, 0, 1<<62, false); len(got) != 3 || got[0] != 5 || got[2] != 3 {
		t.Fatalf("newest-first scan = %v", got)
	}
	if got := seqsOf(g, 0, 1<<62, true); len(got) != 3 || got[0] != 3 || got[2] != 5 {
		t.Fatalf("oldest-first scan = %v", got)
	}
}

func TestRingScanBoundsAndEarlyStop(t *testing.T) {
	g := newRing(10)
	for i := 0; i < 6; i++ {
		g.append(rowAt(t0.Add(time.Duration(i)*time.Second), "s"))
	}
	if got := seqsOf(g, 5, 1<<62, false); len(got) != 2 || got[0] != 6 || got[1] != 5 {
		t.Fatalf("since cursor scan = %v", got)
	}
	if got := seqsOf(g, 7, 1<<62, false); len(got) != 0 {
		t.Fatalf("a cursor past the newest row must visit nothing, got %v", got)
	}
	if got := seqsOf(g, 0, 3, false); len(got) != 3 || got[0] != 3 {
		t.Fatalf("before cursor scan = %v", got)
	}
	visited := 0
	g.scan(0, 1<<62, true, func(r *row) bool {
		visited++
		return visited < 2
	})
	if visited != 2 {
		t.Fatalf("scan must stop when keep returns false, visited %d", visited)
	}
}

func TestRingInternsRepeatedStrings(t *testing.T) {
	g := newRing(10)
	a := strings.Clone("session-1")
	b := strings.Clone("session-1")
	if unsafe.StringData(a) == unsafe.StringData(b) {
		t.Fatal("test needs two backing arrays")
	}
	g.append(rowAt(t0, a))
	g.append(rowAt(t0, b))
	r1, _ := g.at(1)
	r2, _ := g.at(2)
	if unsafe.StringData(r1.sessionID) != unsafe.StringData(r2.sessionID) {
		t.Fatal("equal session IDs must share one backing string")
	}
}

func TestRingPruneAndResize(t *testing.T) {
	g := newRing(10)
	for i := 0; i < 6; i++ {
		g.append(rowAt(t0.Add(time.Duration(i)*time.Minute), "s"+string(rune('a'+i))))
	}
	g.prune(t0.Add(2*time.Minute + time.Second))
	if oldest, newest, _ := g.bounds(); oldest != 4 || newest != 6 {
		t.Fatalf("bounds after prune = %d %d", oldest, newest)
	}
	if _, ok := g.intern["sa"]; ok {
		t.Fatal("intern table must be rebuilt from live rows")
	}
	if _, ok := g.intern["sd"]; !ok {
		t.Fatal("live strings must stay interned")
	}
	g.resize(2)
	if oldest, newest, _ := g.bounds(); oldest != 5 || newest != 6 {
		t.Fatalf("shrink must keep the newest rows, got %d %d", oldest, newest)
	}
	if seq := g.append(rowAt(t0.Add(time.Hour), "z")); seq != 7 {
		t.Fatalf("seq after resize = %d", seq)
	}
	if oldest, newest, _ := g.bounds(); oldest != 6 || newest != 7 {
		t.Fatalf("bounds after append at the new size = %d %d", oldest, newest)
	}
	g.resize(5)
	for i := 0; i < 3; i++ {
		g.append(rowAt(t0.Add(time.Hour), "z"))
	}
	if oldest, newest, _ := g.bounds(); oldest != 6 || newest != 10 {
		t.Fatalf("grow must keep rows and accept more, got %d %d", oldest, newest)
	}
	if got := seqsOf(g, 0, 1<<62, true); len(got) != 5 || got[0] != 6 || got[4] != 10 {
		t.Fatalf("order after grow = %v", got)
	}
}

func TestRingSnapshotRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, snapshotFileName)
	g := newRing(10)
	if _, _, _, dirty := g.copyForSave(); dirty {
		t.Fatal("new ring must not be dirty")
	}
	failed := rowAt(t0, "s")
	failed.flags = rowFailed | rowStream
	failed.status = 529
	failed.errText = "overloaded"
	failed.verdict = Verdict{Kind: VerdictNA, NA: NAFailed}
	g.append(failed)
	g.append(rowAt(t0.Add(time.Second), "s"))
	rows, nextSeq, epoch, dirty := g.copyForSave()
	if !dirty || len(rows) != 2 || nextSeq != 3 || epoch == "" {
		t.Fatalf("copy = %d rows next %d epoch %q dirty %v", len(rows), nextSeq, epoch, dirty)
	}
	if _, _, _, dirty = g.copyForSave(); dirty {
		t.Fatal("copy must clear dirty")
	}
	lanes := []persistedLane{{Session: "s", Model: "opus", TouchedAt: t0.UnixMilli(), Print: []byte{1}}}
	data := buildSnapshot(rows, nextSeq, epoch, lanes, t0)
	if err := writeSnapshot(&g.fileMu, path, data); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("temp files must be cleaned up, dir has %d entries", len(entries))
	}

	back := newRing(10)
	gotLanes, err := back.load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotLanes) != 1 || gotLanes[0].Session != "s" {
		t.Fatalf("lanes = %+v", gotLanes)
	}
	if oldest, newest, gotEpoch := back.bounds(); oldest != 1 || newest != 2 || gotEpoch != epoch {
		t.Fatalf("restored bounds = %d %d %q (want epoch %q)", oldest, newest, gotEpoch, epoch)
	}
	r1, _ := back.at(1)
	if r1.flags != rowFailed|rowStream || r1.status != 529 || r1.errText != "overloaded" || r1.verdict != failed.verdict || r1.requestID != failed.requestID || r1.traceID != "tr" {
		t.Fatalf("restored failed row = %+v", r1)
	}
	r2, _ := back.at(2)
	if r2.read != 5 || r2.verdict.Kind != VerdictHit || r2.verdict.Expected != 5 || r2.sessionID != "s" || r2.agent != "claude-cli" {
		t.Fatalf("restored row = %+v", r2)
	}
	if seq := back.append(rowAt(t0, "s")); seq != 3 {
		t.Fatalf("seq must continue after load, got %d", seq)
	}

	// A smaller ring keeps only the newest rows of the file.
	small := newRing(1)
	if _, err = small.load(path); err != nil {
		t.Fatal(err)
	}
	if oldest, newest, _ := small.bounds(); oldest != 2 || newest != 2 {
		t.Fatalf("small ring bounds = %d %d", oldest, newest)
	}

	// Missing file: fresh epoch, no error. Corrupt file: error, fresh epoch.
	fresh := newRing(10)
	if _, err = fresh.load(filepath.Join(dir, "missing")); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	corrupt := newRing(10)
	if _, err = corrupt.load(path); err == nil {
		t.Fatal("corrupt snapshot must report an error")
	}
	if _, _, gotEpoch := corrupt.bounds(); gotEpoch == epoch || gotEpoch == "" {
		t.Fatalf("corrupt load must keep a fresh epoch, got %q", gotEpoch)
	}
}
