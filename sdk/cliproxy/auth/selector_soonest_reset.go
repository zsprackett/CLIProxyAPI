package auth

import (
	"context"
	"strconv"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// weeklyWindowMinutes is the length of the weekly quota window advertised by Codex.
const weeklyWindowMinutes = 7 * 24 * 60

// SoonestResetSelector prefers the credential whose weekly (7-day) quota window resets
// soonest, so quota that would otherwise expire unused at the reset is consumed first.
// Once that credential is exhausted it enters cooldown and the next-soonest one is used.
//
// Reset times come from the credential's quota snapshot. A window scoped to the
// requested model's family (Claude's Fable 7d_oi window) is preferred over the
// credential-wide one. Credentials without a known future reset, either never observed
// or with a reset that already passed, are picked first so a single request can learn
// their current window. Ties fall back to ID order, which makes providers without quota
// signals behave like fill-first.
type SoonestResetSelector struct{}

// Pick selects the available auth whose weekly quota window resets soonest.
func (s *SoonestResetSelector) Pick(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	_ = opts
	now := time.Now()
	available, err := getSelectorAvailableAuths(ctx, auths, provider, model, now)
	if err != nil {
		return nil, err
	}
	available = preferCodexWebsocketAuths(ctx, provider, available)
	var picked *Auth
	var pickedReset time.Time
	for _, auth := range available {
		resetAt := weeklyQuotaResetAt(auth, model, now)
		if picked == nil || resetAt.Before(pickedReset) {
			picked = auth
			pickedReset = resetAt
		}
	}
	if picked == nil {
		return nil, &Error{Code: "auth_not_found", Message: "no auth candidates"}
	}
	return picked, nil
}

// weeklyResetRank orders the windows that can stand in for the weekly reset:
// a scoped weekly window first, then the credential-wide one, then the Codex
// monthly window for accounts that expose the weekly limit as a longer primary.
func weeklyResetRank(class WindowClass, scoped bool) int {
	switch class {
	case "claude:7d", "claude:7d_oi", "codex:weekly":
		if scoped {
			return 0
		}
		return 1
	case "codex:monthly":
		return 2
	default:
		return -1
	}
}

// weeklyQuotaResetAt returns the observed weekly quota reset time for auth serving model,
// or the zero time when no future reset is known.
func weeklyQuotaResetAt(auth *Auth, model string, now time.Time) time.Time {
	if auth == nil {
		return time.Time{}
	}
	provider := auth.QuotaSnapshot.Provider
	scope := quotaScopeOf(provider, model)
	bestRank := -1
	var resetAt time.Time
	for _, window := range auth.QuotaSnapshot.Windows {
		if window.ModelScope != "" && window.ModelScope != scope {
			continue
		}
		if !window.ResetAt.After(now) {
			continue
		}
		rank := weeklyResetRank(window.Class(provider), window.ModelScope != "")
		if rank < 0 {
			continue
		}
		if bestRank < 0 || rank < bestRank {
			bestRank = rank
			resetAt = window.ResetAt
		}
	}
	return resetAt
}

// parseQuotaResetAt resolves a reset time from an absolute unix timestamp or, failing
// that, from a relative seconds value anchored at the observation time.
func parseQuotaResetAt(resetAt, resetAfterSeconds string, observedAt time.Time) time.Time {
	if unix, errParse := strconv.ParseFloat(resetAt, 64); errParse == nil && unix > 0 {
		return time.Unix(int64(unix), 0)
	}
	if parsed, errParse := time.Parse(time.RFC3339, resetAt); errParse == nil {
		return parsed
	}
	if observedAt.IsZero() {
		return time.Time{}
	}
	if seconds, errParse := strconv.ParseFloat(resetAfterSeconds, 64); errParse == nil && seconds >= 0 {
		return observedAt.Add(time.Duration(seconds * float64(time.Second)))
	}
	return time.Time{}
}
