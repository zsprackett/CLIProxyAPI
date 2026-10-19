package auth

import (
	"math"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func quotaTestHeaders(kv ...string) http.Header {
	h := make(http.Header, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

func quotaTestSnapshot(t *testing.T, provider, model string, now time.Time, kv ...string) QuotaSnapshot {
	t.Helper()
	var snapshot QuotaSnapshot
	if !snapshot.ObserveResponseHeaders(provider, model, quotaTestHeaders(kv...), now) {
		t.Fatalf("ObserveResponseHeaders(%s, %s) reported no change", provider, model)
	}
	return snapshot
}

func windowsByClass(windows []QuotaWindow) map[string]QuotaWindow {
	out := make(map[string]QuotaWindow, len(windows))
	for _, w := range windows {
		out[w.Class] = w
	}
	return out
}

func TestQuotaWindowsForAuthClaude(t *testing.T) {
	now := time.Now()
	reset := strconv.FormatInt(now.Add(3*time.Hour).Unix(), 10)
	auth := &Auth{
		Provider: "claude",
		QuotaSnapshot: quotaTestSnapshot(t, "claude", "claude-fable-5-1", now,
			"Anthropic-Ratelimit-Unified-5h-Utilization", "0.25",
			"Anthropic-Ratelimit-Unified-5h-Reset", reset,
			"Anthropic-Ratelimit-Unified-7d-Utilization", "0.55",
			"Anthropic-Ratelimit-Unified-7d_oi-Utilization", "0.4",
		),
	}
	got := windowsByClass(QuotaWindowsForAuth(auth))
	if len(got) != 3 {
		t.Fatalf("windows = %+v, want 5h, 7d and 7d_oi", got)
	}
	if w := got["claude:5h"]; w.UsedPercent != 25 || w.ResetAt.Unix() != now.Add(3*time.Hour).Unix() || w.Window != 5*time.Hour || w.ModelScope != "" {
		t.Fatalf("5h window = %+v", w)
	}
	if w := got["claude:7d"]; w.UsedPercent != 55 || w.ModelScope != "" {
		t.Fatalf("7d window = %+v", w)
	}
	if w := got["claude:7d_oi"]; math.Abs(w.UsedPercent-40) > 1e-9 || w.ModelScope != "fable" {
		t.Fatalf("7d_oi window = %+v", w)
	}
}

func TestQuotaWindowsForAuthClaudeDropsScopedWindowForOtherModels(t *testing.T) {
	auth := &Auth{
		Provider: "claude",
		QuotaSnapshot: quotaTestSnapshot(t, "claude", "claude-opus-4-6", time.Now(),
			"Anthropic-Ratelimit-Unified-7d-Utilization", "0.5",
			"Anthropic-Ratelimit-Unified-7d_oi-Utilization", "0.9",
		),
	}
	got := windowsByClass(QuotaWindowsForAuth(auth))
	if len(got) != 1 || got["claude:7d"].UsedPercent != 50 {
		t.Fatalf("windows = %+v, want only 7d", got)
	}
}

func TestQuotaWindowsForAuthCodex(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name    string
		signals []string
		want    map[string]float64
	}{
		{"primary and secondary", []string{
			"X-Codex-Primary-Used-Percent", "12", "X-Codex-Primary-Window-Minutes", "300", "X-Codex-Primary-Reset-After-Seconds", "600",
			"X-Codex-Secondary-Used-Percent", "40", "X-Codex-Secondary-Window-Minutes", "10080",
		}, map[string]float64{"codex:5h": 12, "codex:weekly": 40}},
		{"weekly as primary", []string{
			"X-Codex-Primary-Used-Percent", "70", "X-Codex-Primary-Window-Minutes", "10080",
		}, map[string]float64{"codex:weekly": 70}},
		{"monthly", []string{
			"X-Codex-Primary-Used-Percent", "5", "X-Codex-Primary-Window-Minutes", "43200",
		}, map[string]float64{"codex:monthly": 5}},
		{"missing window length", []string{"X-Codex-Primary-Used-Percent", "5"}, map[string]float64{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := windowsByClass(QuotaWindowsForAuth(&Auth{Provider: "codex", QuotaSnapshot: quotaTestSnapshot(t, "codex", "", now, tc.signals...)}))
			if len(got) != len(tc.want) {
				t.Fatalf("windows = %+v, want %v", got, tc.want)
			}
			for class, used := range tc.want {
				if got[class].UsedPercent != used {
					t.Fatalf("%s used = %v, want %v", class, got[class].UsedPercent, used)
				}
			}
		})
	}
	got := windowsByClass(QuotaWindowsForAuth(&Auth{Provider: "codex", QuotaSnapshot: quotaTestSnapshot(t, "codex", "", now, cases[0].signals...)}))
	if reset := got["codex:5h"].ResetAt; !reset.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("relative reset = %v, want observed+10m", reset)
	}
}

func TestQuotaWindowsForAuthXAIAndDevin(t *testing.T) {
	now := time.Now()
	var xaiSnapshot QuotaSnapshot
	xaiObs, ok := fromHeadersAdapter("xai", quotaTestHeaders(
		"X-Xai-Billing-Period-Type", "monthly", "X-Xai-Billing-Used-Percent", "33.5", "X-Xai-Billing-Window-Minutes", "44640"), now)
	if !ok || !xaiSnapshot.Observe(xaiObs) {
		t.Fatal("xai billing headers produced no observation")
	}
	xai := windowsByClass(QuotaWindowsForAuth(&Auth{Provider: "xai", QuotaSnapshot: xaiSnapshot}))
	if w := xai["xai:monthly"]; w.UsedPercent != 33.5 || w.Window != 44640*time.Minute {
		t.Fatalf("xai window = %+v", xai)
	}

	dailyReset := now.Add(5 * time.Hour).UTC().Truncate(time.Second)
	weeklyReset := now.Add(48 * time.Hour).UTC().Truncate(time.Second)
	var devinSnapshot QuotaSnapshot
	if !devinSnapshot.Observe(DevinStatusObservation(SourceRefresh, "pro", 80, 60, dailyReset, weeklyReset, time.Time{}, time.Time{}, now)) {
		t.Fatal("devin observation reported no change")
	}
	got := windowsByClass(QuotaWindowsForAuth(&Auth{Provider: "devin", QuotaSnapshot: devinSnapshot}))
	if len(got) != 2 {
		t.Fatalf("devin windows = %+v, want daily and weekly", got)
	}
	if w := got["devin:daily"]; w.UsedPercent != 20 || !w.ResetAt.Equal(dailyReset) {
		t.Fatalf("devin daily = %+v", w)
	}
	if w := got["devin:weekly"]; w.UsedPercent != 40 || !w.ResetAt.Equal(weeklyReset) {
		t.Fatalf("devin weekly = %+v", w)
	}
}

func TestQuotaWindowsForAuthIgnoresUnusableSnapshots(t *testing.T) {
	if got := QuotaWindowsForAuth(&Auth{Provider: "claude"}); got != nil {
		t.Fatalf("empty snapshot produced %+v", got)
	}
	var unsupported QuotaSnapshot
	if unsupported.ObserveResponseHeaders("gemini", "", quotaTestHeaders("X-Anything", "1"), time.Now()) {
		t.Fatal("unsupported provider reported an observation")
	}
	if got := QuotaWindowsForAuth(&Auth{Provider: "gemini", QuotaSnapshot: unsupported}); got != nil {
		t.Fatalf("unsupported provider produced %+v", got)
	}
	got := QuotaWindowsForAuth(&Auth{Provider: "claude", QuotaSnapshot: quotaTestSnapshot(t, "claude", "", time.Now(), "Anthropic-Ratelimit-Unified-5h-Utilization", "1.7")})
	if len(got) != 1 || got[0].UsedPercent != 100 {
		t.Fatalf("utilization above 100%% should clamp: %+v", got)
	}
}

func TestQuotaWindowsForAuthSkipsPassedResetsAndUnknownPercent(t *testing.T) {
	now := time.Now()
	passed := strconv.FormatInt(now.Add(-time.Hour).Unix(), 10)
	future := strconv.FormatInt(now.Add(time.Hour).Unix(), 10)
	snapshot := quotaTestSnapshot(t, "claude", "", now,
		"Anthropic-Ratelimit-Unified-5h-Utilization", "0.3",
		"Anthropic-Ratelimit-Unified-5h-Reset", passed,
		"Anthropic-Ratelimit-Unified-7d-Reset", future,
	)
	if got := QuotaWindowsForAuth(&Auth{Provider: "claude", QuotaSnapshot: snapshot}); len(got) != 0 {
		t.Fatalf("passed reset and unknown percent should be skipped: %+v", got)
	}
}
