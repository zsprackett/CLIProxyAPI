package auth

import (
	"math"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func quotaSignals(observedAt time.Time, kv ...string) QuotaState {
	signals := make(map[string]string, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		signals[http.CanonicalHeaderKey(kv[i])] = kv[i+1]
	}
	return QuotaState{ObservedAt: observedAt, Signals: signals}
}

func windowsByClass(windows []QuotaWindow) map[string]QuotaWindow {
	out := make(map[string]QuotaWindow, len(windows))
	for _, w := range windows {
		out[w.Class] = w
	}
	return out
}

func TestQuotaWindowsForAuthClaude(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	reset := strconv.FormatInt(now.Add(3*time.Hour).Unix(), 10)
	auth := &Auth{
		Provider: "claude",
		Quota: quotaSignals(now,
			"Anthropic-Ratelimit-Unified-5h-Utilization", "0.25",
			"Anthropic-Ratelimit-Unified-5h-Reset", reset,
			"Anthropic-Ratelimit-Unified-7d-Utilization", "0.5",
			// 7d_oi is ignored on the credential-wide snapshot.
			"Anthropic-Ratelimit-Unified-7d_oi-Utilization", "0.9",
		),
		ModelStates: map[string]*ModelState{
			"claude-fable-5-1": {Quota: quotaSignals(now.Add(time.Minute),
				"Anthropic-Ratelimit-Unified-7d_oi-Utilization", "0.4",
				"Anthropic-Ratelimit-Unified-7d-Utilization", "0.55")},
		},
	}
	got := windowsByClass(QuotaWindowsForAuth(auth))
	if len(got) != 3 {
		t.Fatalf("windows = %+v, want 5h, 7d and 7d_oi", got)
	}
	if w := got["claude:5h"]; w.UsedPercent != 25 || w.ResetAt.Unix() != now.Add(3*time.Hour).Unix() || w.Window != 5*time.Hour {
		t.Fatalf("5h window = %+v", w)
	}
	if w := got["claude:7d"]; w.UsedPercent != 55 || !w.ModelScoped {
		t.Fatalf("7d window should come from the newer model snapshot: %+v", w)
	}
	if w := got["claude:7d_oi"]; math.Abs(w.UsedPercent-40) > 1e-9 || !w.ModelScoped {
		t.Fatalf("7d_oi window = %+v", w)
	}
}

func TestQuotaWindowsForAuthCodex(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
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
			got := windowsByClass(QuotaWindowsForAuth(&Auth{Provider: "codex", Quota: quotaSignals(now, tc.signals...)}))
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
	got := windowsByClass(QuotaWindowsForAuth(&Auth{Provider: "codex", Quota: quotaSignals(now, cases[0].signals...)}))
	if reset := got["codex:5h"].ResetAt; !reset.Equal(now.Add(10 * time.Minute)) {
		t.Fatalf("relative reset = %v, want observed+10m", reset)
	}
}

func TestQuotaWindowsForAuthXAIAndDevin(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	xai := windowsByClass(QuotaWindowsForAuth(&Auth{Provider: "xai", Quota: quotaSignals(now,
		"X-Xai-Billing-Period-Type", "monthly", "X-Xai-Billing-Used-Percent", "33.5", "X-Xai-Billing-Window-Minutes", "44640")}))
	if w := xai["xai:monthly"]; w.UsedPercent != 33.5 || w.Window != 44640*time.Minute {
		t.Fatalf("xai window = %+v", xai)
	}

	resetAt := now.Add(5 * time.Hour).UTC()
	devin := &Auth{Provider: "devin", Quota: QuotaState{ObservedAt: now, Signals: map[string]string{
		"daily_quota_remaining_percent":  "80%",
		"weekly_quota_remaining_percent": "bogus",
		"daily_quota_reset_at":           resetAt.Format(time.RFC3339),
	}}}
	got := windowsByClass(QuotaWindowsForAuth(devin))
	if len(got) != 1 {
		t.Fatalf("devin windows = %+v, want only daily", got)
	}
	if w := got["devin:daily"]; w.UsedPercent != 20 || !w.ResetAt.Equal(resetAt) {
		t.Fatalf("devin daily = %+v", w)
	}
}

func TestQuotaWindowsForAuthIgnoresUnusableSnapshots(t *testing.T) {
	if got := QuotaWindowsForAuth(&Auth{Provider: "claude", Quota: quotaSignals(time.Time{}, "Anthropic-Ratelimit-Unified-5h-Utilization", "0.1")}); got != nil {
		t.Fatalf("snapshot without observation time produced %+v", got)
	}
	if got := QuotaWindowsForAuth(&Auth{Provider: "gemini", Quota: quotaSignals(time.Now(), "X-Anything", "1")}); got != nil {
		t.Fatalf("unsupported provider produced %+v", got)
	}
	got := QuotaWindowsForAuth(&Auth{Provider: "claude", Quota: quotaSignals(time.Now(), "Anthropic-Ratelimit-Unified-5h-Utilization", "1.7")})
	if len(got) != 1 || got[0].UsedPercent != 100 {
		t.Fatalf("utilization above 100%% should clamp: %+v", got)
	}
}
