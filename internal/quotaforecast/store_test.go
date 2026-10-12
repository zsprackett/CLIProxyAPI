package quotaforecast

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

var testNow = time.Unix(1_800_000_000, 0).UTC()

func window(class string, used float64, observed, reset time.Time) coreauth.QuotaWindow {
	return coreauth.QuotaWindow{Class: class, Provider: strings.Split(class, ":")[0], UsedPercent: used, ObservedAt: observed, ResetAt: reset, Window: 7 * 24 * time.Hour}
}

func TestObserveDedupesAndHeartbeats(t *testing.T) {
	h := newHistory()
	reset := testNow.Add(48 * time.Hour)
	steps := []struct {
		at   time.Duration
		used float64
		want bool
	}{
		{0, 10, true},
		{time.Minute, 10, false},          // unchanged
		{2 * time.Minute, 12, true},       // changed
		{2 * time.Minute, 15, false},      // not newer
		{time.Minute, 15, false},          // older
		{17 * time.Minute, 12, true},      // heartbeat
		{18 * time.Minute, 12.004, false}, // below precision
	}
	for i, step := range steps {
		if got := h.observe("a", window("claude:7d", step.used, testNow.Add(step.at), reset)); got != step.want {
			t.Fatalf("step %d: observe = %v, want %v", i, got, step.want)
		}
	}
	if n := len(h.series[seriesKey("a", "claude:7d")].Points); n != 3 {
		t.Fatalf("points = %d, want 3", n)
	}
	if !h.observe("a", window("claude:7d", 12, testNow.Add(19*time.Minute), reset.Add(time.Hour))) {
		t.Fatal("a changed reset time must be recorded")
	}
}

func TestCompactThinsOldPointsButKeepsResetBoundaries(t *testing.T) {
	h := newHistory()
	key := seriesKey("a", "claude:7d")
	old := testNow.Add(-72 * time.Hour).Truncate(thinnedBucket)
	h.series[key] = &Series{AuthID: "a", Class: "claude:7d", Points: []Point{
		{T: testNow.Add(-30 * 24 * time.Hour).Unix(), Used: 1, Reset: 1},    // beyond retention
		{T: old.Unix(), Used: 10, Reset: 100},                               // thinned away
		{T: old.Add(time.Minute).Unix(), Used: 11, Reset: 100},              // last of its cycle: kept
		{T: old.Add(2 * time.Minute).Unix(), Used: 1, Reset: 100 + 7*86400}, // first of next cycle: kept
		{T: old.Add(3 * time.Minute).Unix(), Used: 2, Reset: 100 + 7*86400}, // thinned away
		{T: old.Add(4 * time.Minute).Unix(), Used: 3, Reset: 100 + 7*86400}, // last in bucket: kept
		{T: testNow.Add(-time.Hour).Unix(), Used: 20, Reset: 100 + 7*86400}, // recent: kept
		{T: testNow.Add(-59 * time.Minute).Unix(), Used: 21, Reset: 100 + 7*86400},
	}}
	h.usage[usageKey{Hour: testNow.Add(-30 * 24 * time.Hour).Unix(), AuthID: "a"}] = &UsageBucket{Hour: testNow.Add(-30 * 24 * time.Hour).Unix(), AuthID: "a"}
	h.compact(testNow, 14*24*time.Hour)
	var used []float64
	for _, p := range h.series[key].Points {
		used = append(used, p.Used)
	}
	if want := []float64{11, 1, 3, 20, 21}; len(used) != len(want) || used[0] != 11 || used[1] != 1 || used[2] != 3 {
		t.Fatalf("kept used values = %v, want %v", used, want)
	}
	if len(h.usage) != 0 {
		t.Fatal("expired usage must be dropped")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), historyFileName)
	if strings.HasSuffix(path, ".json") {
		t.Fatal("history file must not look like an auth file")
	}
	h := newHistory()
	h.observe("a", window("codex:weekly", 40, testNow, testNow.Add(time.Hour)))
	h.addUsage(UsageBucket{Hour: testNow.Truncate(time.Hour).Unix(), AuthID: "a", Model: "m", KeyHash: "abc", Agent: "x", Requests: 1, Tokens: 100})
	h.alerts["codex:weekly"] = &alertRecord{Class: "codex:weekly", State: alertFiring, RearmAt: 5}
	if err := h.save(path, testNow); err != nil {
		t.Fatal(err)
	}
	loaded := newHistory()
	if err := loaded.load(path); err != nil {
		t.Fatal(err)
	}
	series, usage, alerts, since := loaded.snapshot("")
	if len(series) != 1 || series[0].Points[0].Used != 40 || len(usage) != 1 || usage[0].Tokens != 100 || len(alerts) != 1 || since != testNow.Unix() {
		t.Fatalf("round trip lost data: %+v %+v %+v %d", series, usage, alerts, since)
	}
	empty := newHistory()
	empty.compact(testNow, time.Hour)
	if _, _, _, since := empty.snapshot(""); since != 0 {
		t.Fatalf("empty history reports recording since %d", since)
	}
	if err := newHistory().load(filepath.Join(t.TempDir(), "missing.gz")); err != nil {
		t.Fatalf("missing file must load as empty: %v", err)
	}
}
