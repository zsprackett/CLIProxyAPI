package auth

import (
	"math"
	"strconv"
	"time"
)

// QuotaWindow is one pooled quota window read from a credential's snapshot.
// Class identifies windows that can be pooled across credentials of the same
// provider, e.g. "claude:7d" or "codex:5h". ModelScope is "" for a
// credential-wide window.
type QuotaWindow struct {
	Class       string
	Provider    string
	ModelScope  string
	UsedPercent float64
	ResetAt     time.Time
	Window      time.Duration
	ObservedAt  time.Time
}

// QuotaWindowsForAuth returns the routable windows of the credential's
// snapshot, one per class and scope, the newest observation winning.
func QuotaWindowsForAuth(auth *Auth) []QuotaWindow {
	if auth == nil {
		return nil
	}
	windows := auth.QuotaSnapshot.RoutingWindows(time.Now())
	if len(windows) == 0 {
		return nil
	}
	type key struct{ class, scope string }
	byKey := make(map[key]int, len(windows))
	out := make([]QuotaWindow, 0, len(windows))
	for _, window := range windows {
		k := key{window.Class, window.ModelScope}
		if i, ok := byKey[k]; ok {
			if window.ObservedAt.After(out[i].ObservedAt) {
				out[i] = window
			}
			continue
		}
		byKey[k] = len(out)
		out = append(out, window)
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
