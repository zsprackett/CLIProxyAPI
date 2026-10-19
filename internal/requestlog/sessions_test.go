package requestlog

import (
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/requestlog/prompt"
)

func TestTrackerLanesPerModel(t *testing.T) {
	tr := newTracker(10)
	p := claudePrintOf(claudeOpts{messages: []string{"hi"}})
	if v := tr.observe(obsAt(t0, "s", "opus", "a", 100, 0, 5000, p)); v.Kind != VerdictCold {
		t.Fatalf("first opus = %+v", v)
	}
	// A helper model in the same session cannot read the opus cache: it is cold
	// (model_changed) and gets its own lane; the opus baseline stays warm.
	if v := tr.observe(obsAt(t0.Add(time.Second*2), "s", "haiku", "a", 100, 0, 0, p)); v.Kind != VerdictCold || v.Primary != CauseModel {
		t.Fatalf("first haiku = %+v", v)
	}
	if v := tr.observe(obsAt(t0.Add(time.Second*4), "s", "opus", "a", 10, 5000, 0, p)); v.Kind != VerdictHit {
		t.Fatalf("opus after haiku = %+v", v)
	}
	sess := tr.sessions["s"]
	if len(sess.lanes) != 2 || sess.lanes[0].model != "opus" || sess.lanes[1].model != "haiku" {
		t.Fatalf("lanes must be most recently touched first, got %+v", sess.lanes)
	}
	if tr.hashes != 2*p.Size() {
		t.Fatalf("hashes = %d, want %d", tr.hashes, 2*p.Size())
	}
	for i, model := range []string{"m3", "m4", "m5"} {
		tr.observe(obsAt(t0.Add(time.Second*time.Duration(6+i)), "s", model, "a", 100, 0, 0, p))
	}
	if len(sess.lanes) != maxLanes || sess.lanes[maxLanes-1].model != "opus" {
		t.Fatalf("fifth model must drop the least recently touched lane (haiku), got %+v", sess.lanes)
	}
	if tr.hashes != maxLanes*p.Size() {
		t.Fatalf("hashes after lane drop = %d", tr.hashes)
	}
}

func TestTrackerIgnoresUnjudgedRecords(t *testing.T) {
	tr := newTracker(10)
	p := claudePrintOf(claudeOpts{messages: []string{"hi"}})
	failed := obsAt(t0, "s", "opus", "a", 1, 0, 0, p)
	failed.failed = true
	tr.observe(failed)
	tr.observe(obsAt(t0, "", "opus", "a", 1, 0, 0, p))
	if len(tr.sessions) != 0 || tr.lru.Len() != 0 || tr.hashes != 0 {
		t.Fatalf("failed and sessionless records must not create sessions: %d sessions", len(tr.sessions))
	}
}

func TestTrackerEvictsLeastRecentlyObserved(t *testing.T) {
	tr := newTracker(2)
	p := claudePrintOf(claudeOpts{messages: []string{"hi"}})
	tr.observe(obsAt(t0, "a", "opus", "x", 100, 0, 5000, p))
	tr.observe(obsAt(t0.Add(time.Second), "b", "opus", "x", 100, 0, 5000, p))
	tr.observe(obsAt(t0.Add(2*time.Second), "a", "opus", "x", 100, 5000, 0, p))
	tr.observe(obsAt(t0.Add(3*time.Second), "c", "opus", "x", 100, 0, 5000, p))
	if _, ok := tr.sessions["b"]; ok {
		t.Fatal("session b was the least recently observed and must be evicted")
	}
	if v := tr.observe(obsAt(t0.Add(4*time.Second), "b", "opus", "x", 100, 5000, 0, p)); v.Kind != VerdictCold {
		t.Fatalf("evicted session must start cold, got %+v", v)
	}
	if len(tr.sessions) != 2 || tr.lru.Len() != 2 || tr.hashes != 2*p.Size() {
		t.Fatalf("tracker = %d sessions, %d lru, %d hashes", len(tr.sessions), tr.lru.Len(), tr.hashes)
	}
	tr.resize(1)
	if len(tr.sessions) != 1 || tr.hashes != p.Size() {
		t.Fatalf("resize must evict down to the new bound, got %d sessions", len(tr.sessions))
	}
}

func TestTrackerHashBudget(t *testing.T) {
	tr := newTracker(100)
	p := claudePrintOf(claudeOpts{messages: messagesN(6)}) // 10 hashes
	tr.hashBudget = 25
	tr.observe(obsAt(t0, "a", "opus", "x", 100, 0, 5000, p))
	tr.observe(obsAt(t0.Add(time.Second), "b", "opus", "x", 100, 0, 5000, p))
	if len(tr.sessions) != 2 || tr.hashes != 20 {
		t.Fatalf("two prints fit the budget: %d sessions %d hashes", len(tr.sessions), tr.hashes)
	}
	tr.observe(obsAt(t0.Add(2*time.Second), "c", "opus", "x", 100, 0, 5000, p))
	if _, ok := tr.sessions["a"]; ok || len(tr.sessions) != 2 || tr.hashes != 20 {
		t.Fatalf("third print must evict the oldest session: %d sessions %d hashes", len(tr.sessions), tr.hashes)
	}
}

func TestTrackerExportRestore(t *testing.T) {
	tr := newTracker(10)
	p := claudePrintOf(claudeOpts{messages: []string{"hi"}})
	tr.observe(obsAt(t0, "fresh", "opus", "x", 100, 0, 5000, p))
	tr.observe(obsAt(t0.Add(-2*time.Hour), "stale", "opus", "x", 100, 0, 5000, p))
	lanes := tr.export(t0.Add(time.Minute))
	if len(lanes) != 1 || lanes[0].Session != "fresh" || lanes[0].Model != "opus" || lanes[0].CachedPrefix != 5000 || lanes[0].TTLSec != 300 {
		t.Fatalf("export = %+v", lanes)
	}
	lanes = append(lanes,
		persistedLane{Session: "bad", Model: "opus", TouchedAt: t0.UnixMilli(), Print: []byte{1, 2}},
		persistedLane{Session: "old", Model: "opus", TouchedAt: t0.Add(-3 * time.Hour).UnixMilli(), Print: lanes[0].Print},
	)
	back := newTracker(10)
	back.restore(lanes, t0.Add(time.Minute))
	if len(back.sessions) != 1 || back.hashes != p.Size() {
		t.Fatalf("restore must keep only the fresh, well-formed lane: %d sessions %d hashes", len(back.sessions), back.hashes)
	}
	if v := back.observe(obsAt(t0.Add(2*time.Minute), "fresh", "opus", "x", 10, 5000, 0, p)); v.Kind != VerdictHit {
		t.Fatalf("restored baseline must judge a hit, got %+v", v)
	}
}

// The auto-mode classifier runs for every agent under the parent session ID and
// one model, with each agent's transcript inside a single message. Interleaved
// calls for two agents must each be judged against their own agent's previous
// call, not against whichever agent ran last.
func TestTrackerInterleavedClassifierTranscripts(t *testing.T) {
	tr := newTracker(10)
	a := []string{"main: fix the bug", "main: ran tests"}
	b := []string{"sub: explore the repo", "sub: read files", "sub: grep"}
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }

	tr.observe(obsAt(at(0), "s", "sonnet", "x", 10, 0, 30000, classifierPrintOf(a, "Bash: go test")))
	tr.observe(obsAt(at(2), "s", "sonnet", "x", 10, 2000, 58000, classifierPrintOf(b, "Read: main.go")))

	a = append(a, "main: tests pass")
	if v := tr.observe(obsAt(at(4), "s", "sonnet", "x", 10, 30000, 200, classifierPrintOf(a, "Bash: git commit"))); v.Kind != VerdictHit {
		t.Fatalf("agent A's second call read its whole cached prefix and must be a hit, got %+v", v)
	}
	b = append(b, "sub: found it")
	if v := tr.observe(obsAt(at(6), "s", "sonnet", "x", 10, 60000, 200, classifierPrintOf(b, "Grep: TODO"))); v.Kind != VerdictHit {
		t.Fatalf("agent B's second call read its whole cached prefix and must be a hit, got %+v", v)
	}
}

// A real edit inside one conversation must still be reported as a partial with
// the edited message, after the lane logic learns to keep several lanes.
func TestTrackerRealEditStillReportsMessageChanged(t *testing.T) {
	tr := newTracker(10)
	msgs := messagesN(20)
	tr.observe(obsAt(t0, "s", "opus", "x", 10, 0, 80000, claudePrintOf(claudeOpts{messages: msgs})))
	edited := append([]string(nil), msgs...)
	edited[14] = "rewritten"
	edited = append(edited, "m20")
	v := tr.observe(obsAt(t0.Add(time.Minute), "s", "opus", "x", 10, 50000, 31000, claudePrintOf(claudeOpts{messages: edited})))
	if v.Kind != VerdictPartial || v.Primary != CauseMessage || v.Segment.unpack() != (prompt.SegmentRef{Kind: prompt.SegmentMessage, Index: 14}) {
		t.Fatalf("edit at message 14 = %+v (segment %+v)", v, v.Segment.unpack())
	}
}
