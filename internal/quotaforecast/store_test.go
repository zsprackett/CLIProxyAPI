package quotaforecast

import (
	"compress/gzip"
	"os"
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
	h.compact(testNow, 14*24*time.Hour)
	var used []float64
	for _, p := range h.series[key].Points {
		used = append(used, p.Used)
	}
	if want := []float64{11, 1, 3, 20, 21}; len(used) != len(want) || used[0] != 11 || used[1] != 1 || used[2] != 3 {
		t.Fatalf("kept used values = %v, want %v", used, want)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), historyFileName)
	if strings.HasSuffix(path, ".json") {
		t.Fatal("history file must not look like an auth file")
	}
	h := newHistory()
	h.observe("a", window("codex:weekly", 40, testNow, testNow.Add(time.Hour)))
	h.alerts["codex:weekly"] = &alertRecord{Class: "codex:weekly", State: alertFiring, RearmAt: 5}
	if err := h.save(path, testNow); err != nil {
		t.Fatal(err)
	}
	loaded := newHistory()
	if err := loaded.load(path); err != nil {
		t.Fatal(err)
	}
	series, alerts, since := loaded.snapshot("")
	if len(series) != 1 || series[0].Points[0].Used != 40 || len(alerts) != 1 || since != testNow.Unix() {
		t.Fatalf("round trip lost data: %+v %+v %d", series, alerts, since)
	}
	empty := newHistory()
	empty.compact(testNow, time.Hour)
	if _, _, since := empty.snapshot(""); since != 0 {
		t.Fatalf("empty history reports recording since %d", since)
	}
	if err := newHistory().load(filepath.Join(t.TempDir(), "missing.gz")); err != nil {
		t.Fatalf("missing file must load as empty: %v", err)
	}
}

// Files written before usage moved to usage history carry a "usage" key.
func TestLoadIgnoresLegacyUsage(t *testing.T) {
	path := filepath.Join(t.TempDir(), historyFileName)
	legacy := `{"version":1,"saved_at":1800000000,"since":1800000000,` +
		`"series":[{"auth_id":"a","provider":"codex","class":"codex:weekly","window_sec":604800,"points":[{"t":1800000000,"u":40}]}],` +
		`"usage":[{"hour":1799996400,"auth_id":"a","model":"m","key_hash":"abc","agent":"x","requests":1,"tokens":100}],` +
		`"alerts":[{"class":"codex:weekly","provider":"codex","state":"firing"}]}`
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(f)
	if _, err = gz.Write([]byte(legacy)); err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	h := newHistory()
	if err = h.load(path); err != nil {
		t.Fatalf("legacy file must load: %v", err)
	}
	series, alerts, since := h.snapshot("")
	if len(series) != 1 || series[0].Points[0].Used != 40 || len(alerts) != 1 || alerts[0].State != alertFiring || since != 1_800_000_000 {
		t.Fatalf("legacy file lost data: %+v %+v %d", series, alerts, since)
	}
}
