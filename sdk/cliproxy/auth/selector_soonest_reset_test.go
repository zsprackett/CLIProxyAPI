package auth

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func soonestResetSnapshot(provider, model string, observedAt time.Time, kv ...string) QuotaSnapshot {
	h := make(http.Header, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	var snapshot QuotaSnapshot
	snapshot.ObserveResponseHeaders(provider, model, h, observedAt)
	return snapshot
}

func claudeAuthResettingAt(id string, resetAt time.Time) *Auth {
	return &Auth{
		ID:       id,
		Provider: "claude",
		QuotaSnapshot: soonestResetSnapshot("claude", "", resetAt.Add(-time.Hour),
			"Anthropic-Ratelimit-Unified-7d-Reset", strconv.FormatInt(resetAt.Unix(), 10)),
	}
}

func TestSoonestResetSelectorPick_PrefersSoonestWeeklyReset(t *testing.T) {
	t.Parallel()

	now := time.Now()
	auths := []*Auth{
		claudeAuthResettingAt("a", now.Add(5*24*time.Hour)),
		claudeAuthResettingAt("b", now.Add(2*time.Hour)),
		claudeAuthResettingAt("c", now.Add(3*24*time.Hour)),
	}

	got, err := (&SoonestResetSelector{}).Pick(context.Background(), "claude", "", cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if got.ID != "b" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "b")
	}
}

func TestSoonestResetSelectorPick_SkipsCoolingDownAuth(t *testing.T) {
	t.Parallel()

	now := time.Now()
	exhausted := claudeAuthResettingAt("b", now.Add(2*time.Hour))
	exhausted.Quota.Exceeded = true
	exhausted.Quota.Reason = "credential_quota"
	exhausted.Quota.NextRecoverAt = now.Add(2 * time.Hour)
	auths := []*Auth{
		claudeAuthResettingAt("a", now.Add(5*24*time.Hour)),
		exhausted,
		claudeAuthResettingAt("c", now.Add(3*24*time.Hour)),
	}

	got, err := (&SoonestResetSelector{}).Pick(context.Background(), "claude", "", cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if got.ID != "c" {
		t.Fatalf("Pick() auth.ID = %q, want %q", got.ID, "c")
	}
}

func TestSoonestResetSelectorPick_ProbesUnknownFirstAndRollsStaleResetForward(t *testing.T) {
	t.Parallel()

	now := time.Now()
	auths := []*Auth{
		claudeAuthResettingAt("a", now.Add(time.Hour)),
		claudeAuthResettingAt("c", now.Add(-time.Hour)),
		{ID: "d", Provider: "claude"},
	}

	got, err := (&SoonestResetSelector{}).Pick(context.Background(), "claude", "", cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if got.ID != "d" {
		t.Fatalf("Pick() auth.ID = %q, want %q (unknown sorts first, the stale reset rolls a week forward)", got.ID, "d")
	}
}

func TestRankSoonestReset_WeeklyCodex(t *testing.T) {
	t.Parallel()

	now := time.Now()
	observed := now.Add(-time.Minute)
	resetAt := now.Add(48 * time.Hour).Truncate(time.Second)

	tests := []struct {
		name    string
		signals []string
		want    time.Time
	}{
		{
			name:    "secondary reset-at",
			signals: []string{"X-Codex-Secondary-Window-Minutes", "10080", "X-Codex-Secondary-Reset-At", strconv.FormatInt(resetAt.Unix(), 10)},
			want:    resetAt,
		},
		{
			name:    "secondary reset-after-seconds",
			signals: []string{"X-Codex-Secondary-Window-Minutes", "10080", "X-Codex-Secondary-Reset-After-Seconds", "3600"},
			want:    observed.Add(time.Hour),
		},
		{
			name: "weekly primary window",
			signals: []string{
				"X-Codex-Primary-Window-Minutes", "10080",
				"X-Codex-Primary-Reset-At", strconv.FormatInt(resetAt.Unix(), 10),
			},
			want: resetAt,
		},
		{
			name: "short primary window ignored",
			signals: []string{
				"X-Codex-Primary-Window-Minutes", "300",
				"X-Codex-Primary-Reset-At", strconv.FormatInt(resetAt.Unix(), 10),
			},
			want: unknownResetLast,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &Auth{ID: "x", Provider: "codex", QuotaSnapshot: soonestResetSnapshot("codex", "", observed, tt.signals...)}
			if got := rankSoonestReset(auth, "", now).weeklyReset; !got.Equal(tt.want) {
				t.Fatalf("rankSoonestReset().weeklyReset = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSoonestResetSelectorPick_PrefersModelScopedWeeklyReset(t *testing.T) {
	t.Parallel()

	now := time.Now()
	// "a" has the sooner credential-wide reset, but "b" has a sooner Fable window
	// in its snapshot, which wins for that model.
	a := claudeAuthResettingAt("a", now.Add(2*24*time.Hour))
	b := &Auth{
		ID:       "b",
		Provider: "claude",
		QuotaSnapshot: soonestResetSnapshot("claude", "claude-fable-5", now,
			"Anthropic-Ratelimit-Unified-7d_oi-Reset", strconv.FormatInt(now.Add(24*time.Hour).Unix(), 10),
			"Anthropic-Ratelimit-Unified-7d-Reset", strconv.FormatInt(now.Add(3*24*time.Hour).Unix(), 10)),
	}
	auths := []*Auth{a, b}

	got, err := (&SoonestResetSelector{}).Pick(context.Background(), "claude", "claude-fable-5", cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if got == nil || got.ID != "b" {
		t.Fatalf("Pick(fable) auth.ID = %v, want %q", got, "b")
	}

	got, err = (&SoonestResetSelector{}).Pick(context.Background(), "claude", "claude-opus-4-7", cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	if got == nil || got.ID != "a" {
		t.Fatalf("Pick(other model) auth.ID = %v, want %q", got, "a")
	}
}

func TestRankSoonestReset_WeeklyIgnoresModelWindowOnCredentialSnapshot(t *testing.T) {
	t.Parallel()

	now := time.Now()
	shared := now.Add(3 * 24 * time.Hour).Truncate(time.Second)
	auth := &Auth{ID: "x", Provider: "claude", QuotaSnapshot: soonestResetSnapshot("claude", "", now,
		"Anthropic-Ratelimit-Unified-7d_oi-Reset", strconv.FormatInt(now.Add(time.Hour).Unix(), 10),
		"Anthropic-Ratelimit-Unified-7d-Reset", strconv.FormatInt(shared.Unix(), 10))}
	if got := rankSoonestReset(auth, "claude-fable-5", now).weeklyReset; !got.Equal(shared) {
		t.Fatalf("rankSoonestReset().weeklyReset = %v, want shared 7d reset %v", got, shared)
	}
}

func TestSoonestResetSelector_UsesSchedulerFastPath(t *testing.T) {
	t.Parallel()

	if !isBuiltInSelector(&SoonestResetSelector{}) {
		t.Fatalf("isBuiltInSelector(SoonestResetSelector) = false, want true")
	}
	if got := selectorStrategy(&SoonestResetSelector{}); got != schedulerStrategySoonestReset {
		t.Fatalf("selectorStrategy() = %v, want %v", got, schedulerStrategySoonestReset)
	}
}

func TestSchedulerPick_SoonestResetHighestPriority(t *testing.T) {
	t.Parallel()

	now := time.Now()
	lowPriority := claudeAuthResettingAt("low", now.Add(time.Hour))
	lowPriority.Attributes = map[string]string{"priority": "0"}
	highLate := claudeAuthResettingAt("high-a", now.Add(5*24*time.Hour))
	highLate.Attributes = map[string]string{"priority": "10"}
	highSoon := claudeAuthResettingAt("high-b", now.Add(2*24*time.Hour))
	highSoon.Attributes = map[string]string{"priority": "10"}
	scheduler := newSchedulerForTest(&SoonestResetSelector{}, lowPriority, highLate, highSoon)

	for index := 0; index < 3; index++ {
		got, errPick := scheduler.pickSingle(context.Background(), "claude", "", cliproxyexecutor.Options{}, nil)
		if errPick != nil {
			t.Fatalf("pickSingle() #%d error = %v", index, errPick)
		}
		if got.ID != "high-b" {
			t.Fatalf("pickSingle() #%d auth.ID = %q, want %q", index, got.ID, "high-b")
		}
	}

	got, errPick := scheduler.pickSingle(context.Background(), "claude", "", cliproxyexecutor.Options{}, map[string]struct{}{"high-b": {}})
	if errPick != nil {
		t.Fatalf("pickSingle() with tried error = %v", errPick)
	}
	if got.ID != "high-a" {
		t.Fatalf("pickSingle() with tried auth.ID = %q, want %q", got.ID, "high-a")
	}
}

func TestSchedulerPick_SoonestResetFollowsObservedSignals(t *testing.T) {
	t.Parallel()

	now := time.Now()
	scheduler := newSchedulerForTest(
		&SoonestResetSelector{},
		claudeAuthResettingAt("a", now.Add(2*24*time.Hour)),
		claudeAuthResettingAt("b", now.Add(4*24*time.Hour)),
	)

	got, errPick := scheduler.pickSingle(context.Background(), "claude", "", cliproxyexecutor.Options{}, nil)
	if errPick != nil {
		t.Fatalf("pickSingle() error = %v", errPick)
	}
	if got.ID != "a" {
		t.Fatalf("pickSingle() auth.ID = %q, want %q", got.ID, "a")
	}

	// A response for "b" reports an earlier weekly reset than "a".
	updated := claudeAuthResettingAt("b", now.Add(time.Hour))
	updated.Generation = 1
	updated.UpdatedAt = now
	scheduler.upsertAuth(updated)

	got, errPick = scheduler.pickSingle(context.Background(), "claude", "", cliproxyexecutor.Options{}, nil)
	if errPick != nil {
		t.Fatalf("pickSingle() after update error = %v", errPick)
	}
	if got.ID != "b" {
		t.Fatalf("pickSingle() after update auth.ID = %q, want %q", got.ID, "b")
	}
}

func TestSchedulerPick_MixedProvidersSoonestReset(t *testing.T) {
	t.Parallel()

	now := time.Now()
	codexSoon := &Auth{
		ID:       "codex-a",
		Provider: "codex",
		QuotaSnapshot: soonestResetSnapshot("codex", "", now,
			"X-Codex-Secondary-Window-Minutes", "10080",
			"X-Codex-Secondary-Reset-At", strconv.FormatInt(now.Add(time.Hour).Unix(), 10)),
	}
	scheduler := newSchedulerForTest(
		&SoonestResetSelector{},
		claudeAuthResettingAt("claude-a", now.Add(3*24*time.Hour)),
		codexSoon,
		claudeAuthResettingAt("claude-b", now.Add(2*24*time.Hour)),
	)

	wantIDs := []string{"codex-a", "claude-b", "claude-a"}
	tried := map[string]struct{}{}
	for index, wantID := range wantIDs {
		got, provider, errPick := scheduler.pickMixed(context.Background(), []string{"claude", "codex"}, "", cliproxyexecutor.Options{}, tried)
		if errPick != nil {
			t.Fatalf("pickMixed() #%d error = %v", index, errPick)
		}
		if got.ID != wantID {
			t.Fatalf("pickMixed() #%d auth.ID = %q, want %q", index, got.ID, wantID)
		}
		if provider != got.Provider {
			t.Fatalf("pickMixed() #%d provider = %q, want %q", index, provider, got.Provider)
		}
		tried[got.ID] = struct{}{}
	}
}

const day = 24 * time.Hour

func unixString(at time.Time) string {
	return strconv.FormatInt(at.Unix(), 10)
}

func fmtFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// claudeAuthWithWindows observes a 5-hour window and a weekly window at 50% use.
func claudeAuthWithWindows(id string, fiveHourUtilization float64, fiveHourReset, weekReset time.Time, extra ...string) *Auth {
	kv := append([]string{
		"Anthropic-Ratelimit-Unified-7d-Utilization", "0.5",
		"Anthropic-Ratelimit-Unified-7d-Reset", unixString(weekReset),
		"Anthropic-Ratelimit-Unified-5h-Utilization", fmtFloat(fiveHourUtilization),
		"Anthropic-Ratelimit-Unified-5h-Reset", unixString(fiveHourReset),
	}, extra...)
	return &Auth{ID: id, Provider: "claude", QuotaSnapshot: soonestResetSnapshot("claude", "", fiveHourReset.Add(-time.Hour), kv...)}
}

func codexWindowHeaders(position string, minutes int, usedPercent float64, resetAt time.Time) []string {
	prefix := "X-Codex-" + position + "-"
	return []string{
		prefix + "Window-Minutes", strconv.Itoa(minutes),
		prefix + "Used-Percent", fmtFloat(usedPercent),
		prefix + "Reset-At", unixString(resetAt),
	}
}

func codexAuthWithHeaders(id string, headerSets ...[]string) *Auth {
	var kv []string
	for _, set := range headerSets {
		kv = append(kv, set...)
	}
	return &Auth{ID: id, Provider: "codex", QuotaSnapshot: soonestResetSnapshot("codex", "", time.Now(), kv...)}
}

// codexWeeklyAuth observes a Codex account whose weekly window is the primary one.
func codexWeeklyAuth(id string, usedPercent float64, resetAt time.Time) *Auth {
	return codexAuthWithHeaders(id, codexWindowHeaders("Primary", weeklyWindowMinutes, usedPercent, resetAt))
}

// pickSoonestReset picks with the selector and checks the scheduler fast path agrees.
func pickSoonestReset(t *testing.T, provider, model string, auths ...*Auth) string {
	t.Helper()
	got, err := (&SoonestResetSelector{}).Pick(context.Background(), provider, model, cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	scheduled, errPick := newSchedulerForTest(&SoonestResetSelector{}, auths...).pickSingle(context.Background(), provider, model, cliproxyexecutor.Options{}, nil)
	if errPick != nil {
		t.Fatalf("pickSingle() error = %v", errPick)
	}
	if scheduled.ID != got.ID {
		t.Fatalf("scheduler picked %q, selector picked %q", scheduled.ID, got.ID)
	}
	return got.ID
}

// An exhausted window skips the credential until that window resets.
func TestSoonestResetSelectorPick_ExhaustionGate(t *testing.T) {
	t.Parallel()

	now := time.Now()
	later := func() *Auth { return claudeAuthWithWindows("b", 0.1, now.Add(4*time.Hour), now.Add(3*day)) }
	rejected := claudeAuthWithWindows("a", 0.5, now.Add(time.Hour), now.Add(day), "Anthropic-Ratelimit-Unified-5h-Status", "rejected")
	// The flag belongs to the most-used window, the 5h one, whose reset has passed.
	codexFiveHourPassed := codexAuthWithHeaders("a",
		codexWindowHeaders("Primary", 300, 90, now.Add(-time.Minute)),
		codexWindowHeaders("Secondary", weeklyWindowMinutes, 40, now.Add(day)),
		[]string{"X-Codex-Limit-Reached", "true"},
	)
	codexFiveHourReached := codexAuthWithHeaders("a",
		codexWindowHeaders("Primary", 300, 90, now.Add(2*time.Hour)),
		codexWindowHeaders("Secondary", weeklyWindowMinutes, 40, now.Add(day)),
		[]string{"X-Codex-Limit-Reached", "true"},
	)

	tests := []struct {
		name     string
		provider string
		auths    []*Auth
		want     string
	}{
		{
			name:     "claude 5h utilization",
			provider: "claude",
			auths:    []*Auth{claudeAuthWithWindows("a", 1, now.Add(time.Hour), now.Add(day)), later()},
			want:     "b",
		},
		{name: "claude 5h rejected", provider: "claude", auths: []*Auth{rejected, later()}, want: "b"},
		{
			name:     "claude 5h reset passed",
			provider: "claude",
			auths:    []*Auth{claudeAuthWithWindows("a", 1, now.Add(-time.Minute), now.Add(day)), later()},
			want:     "a",
		},
		{
			name:     "every candidate gated keeps the ordering",
			provider: "claude",
			auths: []*Auth{
				claudeAuthWithWindows("a", 1, now.Add(time.Hour), now.Add(day)),
				claudeAuthWithWindows("b", 1, now.Add(time.Hour), now.Add(3*day)),
			},
			want: "a",
		},
		{
			name:     "codex used 100",
			provider: "codex",
			auths:    []*Auth{codexWeeklyAuth("a", 100, now.Add(day)), codexWeeklyAuth("b", 10, now.Add(3*day))},
			want:     "b",
		},
		{
			name:     "codex limit reached on the 5h window gates",
			provider: "codex",
			auths:    []*Auth{codexFiveHourReached, codexWeeklyAuth("b", 10, now.Add(3*day))},
			want:     "b",
		},
		{
			name:     "codex limit reached on a passed 5h window does not gate the weekly one",
			provider: "codex",
			auths:    []*Auth{codexFiveHourPassed, codexWeeklyAuth("b", 10, now.Add(3*day))},
			want:     "a",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pickSoonestReset(t, tt.provider, "", tt.auths...); got != tt.want {
				t.Fatalf("Pick() = %q, want %q", got, tt.want)
			}
		})
	}
}

// An exhausted Fable window gates only the Fable model.
func TestSoonestResetSelectorPick_FableGateIsModelScoped(t *testing.T) {
	t.Parallel()

	now := time.Now()
	fable := &Auth{ID: "fable-gate-a", Provider: "claude", QuotaSnapshot: soonestResetSnapshot("claude", "claude-fable-5", now,
		"Anthropic-Ratelimit-Unified-7d_oi-Utilization", "1",
		"Anthropic-Ratelimit-Unified-7d_oi-Reset", unixString(now.Add(2*day)),
		"Anthropic-Ratelimit-Unified-7d-Reset", unixString(now.Add(day)))}
	later := claudeAuthResettingAt("fable-gate-b", now.Add(3*day))
	reg := registry.GetGlobalRegistry()
	for _, id := range []string{fable.ID, later.ID} {
		reg.RegisterClient(id, "claude", []*registry.ModelInfo{{ID: "claude-fable-5"}, {ID: "claude-opus-4-7"}})
		t.Cleanup(func() { reg.UnregisterClient(id) })
	}
	for model, want := range map[string]string{"claude-fable-5": later.ID, "claude-opus-4-7": fable.ID} {
		if got := pickSoonestReset(t, "claude", model, fable, later); got != want {
			t.Fatalf("Pick(%s) = %q, want %q", model, got, want)
		}
	}
}

// A Codex window without a Window-Minutes header has no class and does not gate.
func TestSoonestResetSelectorPick_CodexWindowWithoutLengthDoesNotGate(t *testing.T) {
	t.Parallel()

	now := time.Now()
	a := codexAuthWithHeaders("a",
		[]string{"X-Codex-Primary-Used-Percent", "100", "X-Codex-Primary-Reset-At", unixString(now.Add(time.Hour))},
		codexWindowHeaders("Secondary", weeklyWindowMinutes, 40, now.Add(day)))
	b := codexWeeklyAuth("b", 10, now.Add(3*day))
	if got := pickSoonestReset(t, "codex", "", a, b); got != "a" {
		t.Fatalf("Pick() = %q, want a", got)
	}
}

func TestSoonestResetSelectorPick_UnknownResetsPerProvider(t *testing.T) {
	t.Parallel()

	now := time.Now()
	tests := []struct {
		name     string
		provider string
		auths    []*Auth
		want     string
	}{
		{
			name:     "codex never observed loses to a weekly reset tomorrow",
			provider: "codex",
			auths:    []*Auth{{ID: "a", Provider: "codex"}, codexWeeklyAuth("b", 10, now.Add(day))},
			want:     "b",
		},
		{
			name:     "codex passed reset sorts last",
			provider: "codex",
			auths:    []*Auth{codexWeeklyAuth("a", 10, now.Add(-time.Hour)), codexWeeklyAuth("b", 10, now.Add(5*day))},
			want:     "b",
		},
		{
			name:     "claude never observed still sorts first",
			provider: "claude",
			auths:    []*Auth{claudeAuthResettingAt("a", now.Add(time.Hour)), {ID: "b", Provider: "claude"}},
			want:     "b",
		},
		{
			// 9 days ago rolls forward to 5 days from now, after "b" at 2 days.
			name:     "claude passed reset rolls forward by whole weeks",
			provider: "claude",
			auths:    []*Auth{claudeAuthResettingAt("a", now.Add(-9*day)), claudeAuthResettingAt("b", now.Add(2*day))},
			want:     "b",
		},
		{
			// 13 days ago rolls forward to tomorrow, before "b" at 3 days.
			name:     "claude passed reset rolling forward to tomorrow beats a later reset",
			provider: "claude",
			auths:    []*Auth{claudeAuthResettingAt("a", now.Add(-13*day)), claudeAuthResettingAt("b", now.Add(3*day))},
			want:     "a",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pickSoonestReset(t, tt.provider, "", tt.auths...); got != tt.want {
				t.Fatalf("Pick() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRankSoonestReset_ClaudePassedResetRollsForward(t *testing.T) {
	t.Parallel()

	now := time.Now()
	passed := now.Add(-9 * day).Truncate(time.Second)
	auth := claudeAuthResettingAt("a", passed)
	want := passed.Add(2 * 7 * day)
	if got := rankSoonestReset(auth, "", now).weeklyReset; !got.Equal(want) {
		t.Fatalf("weeklyReset = %v, want %v", got, want)
	}
}
