package auth

import (
	"net/http"
	"strings"
	"time"
)

// pollShowsQuotaCapacity reports whether a polled quota snapshot proves the
// account has room again. Only providers whose full set of windows is reported
// by the poll can prove it; a window that is missing counts as unknown.
func pollShowsQuotaCapacity(provider string, polled QuotaState) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	windows := quotaWindowsFromSnapshot(provider, polled, false)
	signal := func(name string) string {
		return strings.TrimSpace(polled.Signals[http.CanonicalHeaderKey(name)])
	}
	switch provider {
	case "claude":
		seen := make(map[string]bool, 2)
		for _, window := range windows {
			if window.Class != "claude:5h" && window.Class != "claude:7d" {
				continue
			}
			if window.UsedPercent >= 100 {
				return false
			}
			seen[window.Class] = true
		}
		return seen["claude:5h"] && seen["claude:7d"]
	case "codex":
		if len(windows) == 0 {
			return false
		}
		for _, window := range windows {
			if window.UsedPercent >= 100 {
				return false
			}
		}
		if strings.EqualFold(signal("X-Codex-Limit-Reached"), "true") {
			return false
		}
		return !strings.EqualFold(signal("X-Codex-Allowed"), "false")
	default:
		return false
	}
}

// hasActiveQuotaCooldown reports whether the auth, or any of its model states,
// is blocked by a quota cooldown whose deadline is still ahead.
func hasActiveQuotaCooldown(auth *Auth, now time.Time) bool {
	if auth == nil {
		return false
	}
	if quotaCooldownActive(auth.Quota, now) {
		return true
	}
	for _, state := range auth.ModelStates {
		if state != nil && quotaCooldownActive(state.Quota, now) {
			return true
		}
	}
	return false
}

// cooldownRecordsUnchanged compares cooldown records while ignoring UpdatedAt
// on the auth-level record. The poll's own observation bumps auth.UpdatedAt.
// Model records keep their UpdatedAt, which every failure advances.
func cooldownRecordsUnchanged(before, after []CooldownStateRecord) bool {
	strip := func(records []CooldownStateRecord) []CooldownStateRecord {
		out := make([]CooldownStateRecord, len(records))
		copy(out, records)
		for i := range out {
			if out[i].Model == "" {
				out[i].UpdatedAt = time.Time{}
			}
		}
		return out
	}
	return cooldownStateRecordsEqual(strip(before), strip(after))
}

func quotaCooldownActive(quota QuotaState, now time.Time) bool {
	return quota.Exceeded &&
		(quota.Reason == "credential_quota" || quota.Reason == "quota") &&
		quota.NextRecoverAt.After(now)
}
