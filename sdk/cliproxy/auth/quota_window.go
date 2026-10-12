package auth

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// QuotaWindow is one normalized quota window read from a credential's observed
// quota snapshot. Class identifies windows that can be pooled across credentials
// of the same provider, e.g. "claude:7d" or "codex:5h".
type QuotaWindow struct {
	Class       string
	Provider    string
	UsedPercent float64
	ResetAt     time.Time
	Window      time.Duration
	ObservedAt  time.Time
	ModelScoped bool
}

// QuotaWindowsForAuth normalizes the credential-wide and model-scoped quota
// snapshots of auth into quota windows. When several snapshots carry the same
// window, the most recently observed one wins. Model-specific windows (Claude's
// Fable 7d_oi window) are only read from model-scoped snapshots.
func QuotaWindowsForAuth(auth *Auth) []QuotaWindow {
	if auth == nil {
		return nil
	}
	provider := strings.ToLower(strings.TrimSpace(auth.Provider))
	byClass := make(map[string]QuotaWindow)
	add := func(windows []QuotaWindow) {
		for _, window := range windows {
			if existing, ok := byClass[window.Class]; ok && !window.ObservedAt.After(existing.ObservedAt) {
				continue
			}
			byClass[window.Class] = window
		}
	}
	add(quotaWindowsFromSnapshot(provider, auth.Quota, false))
	for _, state := range auth.ModelStates {
		if state != nil {
			add(quotaWindowsFromSnapshot(provider, state.Quota, true))
		}
	}
	if len(byClass) == 0 {
		return nil
	}
	out := make([]QuotaWindow, 0, len(byClass))
	for _, window := range quotaWindowClassOrder {
		if w, ok := byClass[window]; ok {
			out = append(out, w)
		}
	}
	return out
}

// quotaWindowClassOrder fixes the output order of QuotaWindowsForAuth.
var quotaWindowClassOrder = []string{
	"claude:5h", "claude:7d", "claude:7d_oi",
	"codex:5h", "codex:weekly", "codex:monthly",
	"xai:weekly", "xai:monthly",
	"devin:daily", "devin:weekly",
}

func quotaWindowsFromSnapshot(provider string, quota QuotaState, modelScoped bool) []QuotaWindow {
	if len(quota.Signals) == 0 || quota.ObservedAt.IsZero() {
		return nil
	}
	signal := func(name string) string {
		return strings.TrimSpace(quota.Signals[http.CanonicalHeaderKey(name)])
	}
	var out []QuotaWindow
	push := func(class string, used float64, hasUsed bool, resetAt time.Time, window time.Duration) {
		if !hasUsed {
			return
		}
		out = append(out, QuotaWindow{
			Class:       provider + ":" + class,
			Provider:    provider,
			UsedPercent: clampPercent(used),
			ResetAt:     resetAt,
			Window:      window,
			ObservedAt:  quota.ObservedAt,
			ModelScoped: modelScoped,
		})
	}
	switch provider {
	case "claude":
		windows := []struct {
			name   string
			length time.Duration
		}{{"5h", 5 * time.Hour}, {"7d", 7 * 24 * time.Hour}}
		if modelScoped {
			windows = append(windows, struct {
				name   string
				length time.Duration
			}{"7d_oi", 7 * 24 * time.Hour})
		}
		for _, w := range windows {
			prefix := "Anthropic-Ratelimit-Unified-" + w.name + "-"
			utilization, ok := parseQuotaFloat(signal(prefix + "Utilization"))
			push(w.name, utilization*100, ok, parseQuotaResetAt(signal(prefix+"Reset"), "", quota.ObservedAt), w.length)
		}
	case "codex":
		for _, slot := range []string{"Primary", "Secondary"} {
			prefix := "X-Codex-" + slot + "-"
			used, ok := parseQuotaFloat(signal(prefix + "Used-Percent"))
			minutes, okMinutes := parseQuotaFloat(signal(prefix + "Window-Minutes"))
			if !okMinutes || minutes <= 0 {
				continue
			}
			class := "5h"
			switch {
			case minutes > weeklyWindowMinutes:
				class = "monthly"
			case minutes == weeklyWindowMinutes:
				class = "weekly"
			}
			resetAt := parseQuotaResetAt(signal(prefix+"Reset-At"), signal(prefix+"Reset-After-Seconds"), quota.ObservedAt)
			push(class, used, ok, resetAt, time.Duration(minutes)*time.Minute)
		}
	case "xai":
		used, ok := parseQuotaFloat(signal("X-Xai-Billing-Used-Percent"))
		class := "weekly"
		length := 7 * 24 * time.Hour
		if strings.EqualFold(signal("X-Xai-Billing-Period-Type"), "monthly") {
			class = "monthly"
			length = 30 * 24 * time.Hour
		}
		if minutes, okMinutes := parseQuotaFloat(signal("X-Xai-Billing-Window-Minutes")); okMinutes && minutes > 0 {
			length = time.Duration(minutes) * time.Minute
		}
		push(class, used, ok, parseQuotaResetAt(signal("X-Xai-Billing-Reset-At"), "", quota.ObservedAt), length)
	case "devin":
		// Devin's refresh path writes these keys verbatim rather than as headers.
		for _, w := range []struct {
			name   string
			length time.Duration
		}{{"daily", 24 * time.Hour}, {"weekly", 7 * 24 * time.Hour}} {
			remaining, ok := parseQuotaFloat(strings.TrimSuffix(strings.TrimSpace(quota.Signals[w.name+"_quota_remaining_percent"]), "%"))
			resetAt := parseQuotaResetAt(strings.TrimSpace(quota.Signals[w.name+"_quota_reset_at"]), "", quota.ObservedAt)
			push(w.name, 100-remaining, ok, resetAt, w.length)
		}
	}
	return out
}

func parseQuotaFloat(value string) (float64, bool) {
	if value == "" {
		return 0, false
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed != parsed {
		return 0, false
	}
	return parsed, true
}

// clampPercent bounds value to 0-100 and rounds away float noise from fraction scaling.
func clampPercent(value float64) float64 {
	value = math.Round(value*1e6) / 1e6
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
