package auth

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

const (
	// weeklyWindowMinutes is the length of the weekly quota window advertised by Codex.
	weeklyWindowMinutes = 7 * 24 * 60
	week                = 7 * 24 * time.Hour
)

// unknownResetLast orders a credential after every credential with a known reset.
var unknownResetLast = time.Unix(1<<62, 0)

// SoonestResetSelector prefers the credential whose weekly (7-day) quota window resets
// soonest, so quota that would otherwise expire unused at the reset is consumed first.
// Once that credential is exhausted it enters cooldown and the next-soonest one is used.
//
// Reset times come from the passive quota snapshot (Quota.Signals) captured from upstream
// response headers. The requested model's own snapshot is preferred over the credential-wide
// one, so a model-specific weekly window (Claude's Fable 7d_oi window) is honored.
// A credential is skipped while one of its observed windows is exhausted and that window's
// reset is still ahead; when every candidate is skipped this way the ordering alone decides.
//
// An unknown Claude weekly reset sorts first: the window runs on a fixed per-account
// schedule, so one request learns it. A passed Claude reset rolls forward by whole weeks.
// An unknown or passed Codex weekly reset sorts last: that window is usually unstarted and
// starting it early can waste it. Ties fall back to ID order, which makes providers without
// quota signals behave like fill-first.
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

// soonestResetRank orders a credential for soonest-reset. The selector and the scheduler
// fast path both rank with rankSoonestReset, so they pick the same credential.
type soonestResetRank struct {
	// gated is set while an observed window is exhausted and its reset is ahead.
	gated bool
	// weeklyReset is the weekly reset that orders credentials that are not gated.
	weeklyReset time.Time
}

// before reports whether r goes before other: an ungated credential first, then the
// sooner weekly reset.
func (r soonestResetRank) before(other soonestResetRank) bool {
	if r.gated != other.gated {
		return !r.gated
	}
	return r.weeklyReset.Before(other.weeklyReset)
}

// rankSoonestReset ranks auth serving model. The weekly reset is the first future weekly
// reset, model-scoped snapshot first. Without one, a Claude credential uses its first passed
// weekly reset rolled forward by whole weeks, or the zero time when none was observed, and
// any other credential sorts last.
func rankSoonestReset(auth *Auth, model string, now time.Time) soonestResetRank {
	var rank soonestResetRank
	if auth == nil {
		return rank
	}
	var passed time.Time
	for _, window := range soonestResetWindows(auth, model) {
		if !window.resetAt.After(now) {
			if window.weekly && passed.IsZero() {
				passed = window.resetAt
			}
			continue
		}
		if window.exhausted {
			rank.gated = true
		}
		if window.weekly && rank.weeklyReset.IsZero() {
			rank.weeklyReset = window.resetAt
		}
	}
	switch {
	case !rank.weeklyReset.IsZero():
	case !strings.EqualFold(strings.TrimSpace(auth.Provider), "claude"):
		rank.weeklyReset = unknownResetLast
	case !passed.IsZero():
		rank.weeklyReset = passed.Add(week * (now.Sub(passed)/week + 1))
	}
	return rank
}

// quotaWindow is one observed quota window of a credential.
type quotaWindow struct {
	weekly      bool
	exhausted   bool
	usedPercent float64
	resetAt     time.Time
}

// soonestResetWindows returns the windows observed for auth serving model: the model-scoped
// snapshot first, then the credential-wide snapshot.
func soonestResetWindows(auth *Auth, model string) []quotaWindow {
	var windows []quotaWindow
	if state := existingModelState(auth, model); state != nil {
		windows = observedQuotaWindows(auth.Provider, state.Quota, true)
	}
	return append(windows, observedQuotaWindows(auth.Provider, auth.Quota, false)...)
}

// observedQuotaWindows reads the windows of one quota snapshot that have a known reset.
// Model-specific windows are only read from model-scoped snapshots, because the
// credential-wide snapshot may have been captured from a different model's response.
func observedQuotaWindows(provider string, quota QuotaState, modelScoped bool) []quotaWindow {
	signal := func(name string) string {
		return strings.TrimSpace(quota.Signals[http.CanonicalHeaderKey(name)])
	}
	var windows []quotaWindow
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude":
		names := []string{"5h", "7d"}
		if modelScoped {
			names = []string{"7d_oi", "5h", "7d"}
		}
		for _, name := range names {
			prefix := "Anthropic-Ratelimit-Unified-" + name + "-"
			resetAt := parseQuotaResetAt(signal(prefix+"Reset"), "", quota.ObservedAt)
			if resetAt.IsZero() {
				continue
			}
			utilization, _ := strconv.ParseFloat(signal(prefix+"Utilization"), 64)
			rejected := strings.EqualFold(signal(prefix+"Status"), "rejected")
			windows = append(windows, quotaWindow{weekly: name != "5h", exhausted: utilization >= 1 || rejected, resetAt: resetAt})
		}
	case "codex":
		// Either position can carry the weekly window, so a window is weekly by its length.
		// A window without a length keeps its reset but is not weekly.
		mostUsed := 0.0
		for _, name := range []string{"Primary", "Secondary"} {
			prefix := "X-Codex-" + name + "-"
			resetAt := parseQuotaResetAt(signal(prefix+"Reset-At"), signal(prefix+"Reset-After-Seconds"), quota.ObservedAt)
			if resetAt.IsZero() {
				continue
			}
			minutes, _ := strconv.ParseInt(signal(prefix+"Window-Minutes"), 10, 64)
			used, _ := strconv.ParseFloat(signal(prefix+"Used-Percent"), 64)
			mostUsed = max(mostUsed, used)
			windows = append(windows, quotaWindow{weekly: minutes >= weeklyWindowMinutes, usedPercent: used, resetAt: resetAt})
		}
		// The limit-reached flag names no window; it is the most used one.
		limitReached, _ := strconv.ParseBool(signal("X-Codex-Limit-Reached"))
		for i := range windows {
			used := windows[i].usedPercent
			windows[i].exhausted = used >= 100 || limitReached && used == mostUsed
		}
	}
	return windows
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
