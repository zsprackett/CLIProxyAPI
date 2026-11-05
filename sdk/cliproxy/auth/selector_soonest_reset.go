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
// A credential with an exhausted quota window that has not reset yet (for example a
// spent 5-hour window) sorts behind every credential with room, because it cannot serve
// the request. If every candidate is exhausted, the weekly reset alone decides.
//
// Reset times come from the credential's quota snapshot. A window scoped to the
// requested model's family (Claude's Fable 7d_oi window) is preferred over the
// credential-wide one. A Claude credential never observed is picked first so a single
// request can learn its window, and one whose weekly reset passed is ordered by that
// reset rolled forward by whole weeks. A Codex credential without a known future reset
// is picked last. Ties fall back to ID order, which makes providers without quota
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
	var pickedRank soonestResetRank
	for _, auth := range available {
		rank := rankSoonestReset(auth, model, now)
		if picked == nil || rank.before(pickedRank) {
			picked = auth
			pickedRank = rank
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

// soonestResetRank is the sort key of a credential under SoonestResetSelector.
type soonestResetRank struct {
	// gated is true when an applicable quota window is exhausted until a future reset.
	gated       bool
	weeklyReset time.Time
}

// before reports whether r sorts ahead of o: credentials with room first, then the
// earlier weekly reset.
func (r soonestResetRank) before(o soonestResetRank) bool {
	if r.gated != o.gated {
		return !r.gated
	}
	return r.weeklyReset.Before(o.weeklyReset)
}

// rankSoonestReset ranks auth serving model from its quota snapshot. weeklyReset is the
// observed weekly reset, or the zero time when no future reset is known.
func rankSoonestReset(auth *Auth, model string, now time.Time) soonestResetRank {
	var rank soonestResetRank
	if auth == nil {
		return rank
	}
	provider := auth.QuotaSnapshot.Provider
	if provider == "" {
		provider, _ = providerKindOf(auth.Provider)
	}
	scope := quotaScopeOf(provider, model)
	var applicable []QuotaWindowObs
	maxCodexUsed, codexUsedKnown := 0.0, false
	for _, window := range auth.QuotaSnapshot.Windows {
		class := window.Class(provider)
		if class == "" || (window.ModelScope != "" && window.ModelScope != scope) {
			continue
		}
		applicable = append(applicable, window)
		if provider == ProviderCodex {
			if used, ok := window.Used.Get(); ok && (!codexUsedKnown || used > maxCodexUsed) {
				maxCodexUsed, codexUsedKnown = used, true
			}
		}
	}
	bestRank := -1
	passedRank := -1
	var passedReset time.Time
	for _, window := range applicable {
		if !window.ResetAt.After(now) {
			if class := window.Class(provider); !window.ResetAt.IsZero() && (class == "claude:7d" || class == "claude:7d_oi") {
				if windowRank := weeklyResetRank(class, window.ModelScope != ""); passedRank < 0 || windowRank < passedRank {
					passedRank = windowRank
					passedReset = window.ResetAt
				}
			}
			continue
		}
		if windowExhausted(window, provider, maxCodexUsed, codexUsedKnown) {
			rank.gated = true
		}
		windowRank := weeklyResetRank(window.Class(provider), window.ModelScope != "")
		if windowRank < 0 {
			continue
		}
		if bestRank < 0 || windowRank < bestRank {
			bestRank = windowRank
			rank.weeklyReset = window.ResetAt
		}
	}
	if bestRank < 0 {
		rank.weeklyReset = unknownWeeklyReset(provider, passedReset, now)
	}
	return rank
}

// unknownResetLast sorts after every real reset time.
var unknownResetLast = time.Unix(1<<62, 0)

// unknownWeeklyReset places a credential with no future weekly reset. A Claude weekly
// window runs on a fixed per-account schedule, so a passed reset is rolled forward by
// whole weeks to the next boundary, and a credential never observed sorts first so one
// request learns its window. A Codex weekly window usually starts at first use, so a
// Codex credential with no known reset sorts last, behind those with a known reset:
// starting its window early can waste it. Other providers keep the zero time, which
// sorts first and ties by ID.
func unknownWeeklyReset(provider ProviderKind, passedReset, now time.Time) time.Time {
	switch provider {
	case ProviderClaude:
		if passedReset.IsZero() {
			return time.Time{}
		}
		const week = 7 * 24 * time.Hour
		return passedReset.Add(week * (now.Sub(passedReset)/week + 1))
	case ProviderCodex:
		return unknownResetLast
	default:
		return time.Time{}
	}
}

// windowExhausted reports whether window has no room left. Codex sets one
// limit-reached flag for the whole namespace, so on Codex the flag belongs to the
// most-used window, and only that window counts as exhausted by it.
func windowExhausted(window QuotaWindowObs, provider ProviderKind, maxCodexUsed float64, codexUsedKnown bool) bool {
	used, usedKnown := window.Used.Get()
	if usedKnown && used >= 100 {
		return true
	}
	if !window.LimitReached {
		return false
	}
	if provider != ProviderCodex || !codexUsedKnown {
		return true
	}
	return usedKnown && used == maxCodexUsed
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
