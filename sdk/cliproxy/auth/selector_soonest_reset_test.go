package auth

import (
	"context"
	"strconv"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func claudeAuthResettingAt(id string, resetAt time.Time) *Auth {
	return &Auth{
		ID:       id,
		Provider: "claude",
		Quota: QuotaState{
			ObservedAt: resetAt.Add(-time.Hour),
			Signals: map[string]string{
				"Anthropic-Ratelimit-Unified-7d-Reset": strconv.FormatInt(resetAt.Unix(), 10),
			},
		},
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

// An unknown Claude reset sorts first, an unknown or passed Codex reset sorts last, and a
// passed Claude reset rolls forward by whole weeks.
func TestSoonestResetSelectorPick_UnknownResets(t *testing.T) {
	t.Parallel()

	now := time.Now()
	if got := pickSoonestReset(t, "codex",
		&Auth{ID: "a", Provider: "codex"},
		codexWeeklyAuth("b", 50, now.Add(-time.Hour)),
		codexWeeklyAuth("c", 50, now.Add(6*day)),
	); got != "c" {
		t.Fatalf("codex Pick() = %q, want c", got)
	}
	if got := pickSoonestReset(t, "claude",
		claudeAuthResettingAt("a", now.Add(time.Hour)),
		&Auth{ID: "b", Provider: "claude"},
	); got != "b" {
		t.Fatalf("claude Pick() = %q, want b", got)
	}
	// b's reset passed six days ago, so its next one is in one day.
	if got := pickSoonestReset(t, "claude",
		claudeAuthResettingAt("a", now.Add(2*day)),
		claudeAuthResettingAt("b", now.Add(-6*day)),
	); got != "b" {
		t.Fatalf("claude rolled-forward Pick() = %q, want b", got)
	}
	rolled := rankSoonestReset(claudeAuthResettingAt("b", now.Add(-6*day)), "", now).weeklyReset
	if want := time.Unix(now.Add(-6*day).Unix(), 0).Add(week); !rolled.Equal(want) {
		t.Fatalf("rolled reset = %v, want %v", rolled, want)
	}
}

func TestSoonestResetWeeklyReset_Codex(t *testing.T) {
	t.Parallel()

	now := time.Now()
	observed := now.Add(-time.Minute)
	resetAt := now.Add(48 * time.Hour).Truncate(time.Second)

	tests := []struct {
		name    string
		signals map[string]string
		want    time.Time
	}{
		{
			name: "secondary reset-at",
			signals: map[string]string{
				"X-Codex-Secondary-Window-Minutes": "10080",
				"X-Codex-Secondary-Reset-At":       strconv.FormatInt(resetAt.Unix(), 10),
			},
			want: resetAt,
		},
		{
			name: "secondary reset-after-seconds",
			signals: map[string]string{
				"X-Codex-Secondary-Window-Minutes":      "10080",
				"X-Codex-Secondary-Reset-After-Seconds": "3600",
			},
			want: observed.Add(time.Hour),
		},
		{
			name: "weekly primary before short secondary",
			signals: map[string]string{
				"X-Codex-Primary-Window-Minutes":   "10080",
				"X-Codex-Primary-Reset-At":         strconv.FormatInt(resetAt.Unix(), 10),
				"X-Codex-Secondary-Window-Minutes": "300",
				"X-Codex-Secondary-Reset-At":       strconv.FormatInt(now.Add(time.Hour).Unix(), 10),
			},
			want: resetAt,
		},
		{
			name:    "window without length is not weekly",
			signals: map[string]string{"X-Codex-Secondary-Reset-At": strconv.FormatInt(resetAt.Unix(), 10)},
			want:    unknownResetLast,
		},
		{
			name: "weekly primary window",
			signals: map[string]string{
				"X-Codex-Primary-Window-Minutes": "10080",
				"X-Codex-Primary-Reset-At":       strconv.FormatInt(resetAt.Unix(), 10),
			},
			want: resetAt,
		},
		{
			name: "short primary window ignored",
			signals: map[string]string{
				"X-Codex-Primary-Window-Minutes": "300",
				"X-Codex-Primary-Reset-At":       strconv.FormatInt(resetAt.Unix(), 10),
			},
			want: unknownResetLast,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &Auth{ID: "x", Provider: "codex", Quota: QuotaState{ObservedAt: observed, Signals: tt.signals}}
			if got := rankSoonestReset(auth, "", now).weeklyReset; !got.Equal(tt.want) {
				t.Fatalf("weekly reset = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSoonestResetSelectorPick_PrefersModelScopedWeeklyReset(t *testing.T) {
	t.Parallel()

	now := time.Now()
	// "a" has the sooner credential-wide reset, but "b" has a sooner Fable window
	// recorded on its model-scoped snapshot, which wins for that model.
	a := claudeAuthResettingAt("a", now.Add(2*24*time.Hour))
	b := claudeAuthResettingAt("b", now.Add(3*24*time.Hour))
	b.ModelStates = map[string]*ModelState{
		"claude-fable-5": {
			Quota: QuotaState{
				ObservedAt: now,
				Signals: map[string]string{
					"Anthropic-Ratelimit-Unified-7d_oi-Reset": strconv.FormatInt(now.Add(24*time.Hour).Unix(), 10),
					"Anthropic-Ratelimit-Unified-7d-Reset":    strconv.FormatInt(now.Add(3*24*time.Hour).Unix(), 10),
				},
			},
		},
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

func TestSoonestResetWeeklyReset_IgnoresModelWindowOnCredentialSnapshot(t *testing.T) {
	t.Parallel()

	now := time.Now()
	shared := now.Add(3 * 24 * time.Hour).Truncate(time.Second)
	auth := &Auth{ID: "x", Provider: "claude", Quota: QuotaState{
		ObservedAt: now,
		Signals: map[string]string{
			"Anthropic-Ratelimit-Unified-7d_oi-Reset": strconv.FormatInt(now.Add(time.Hour).Unix(), 10),
			"Anthropic-Ratelimit-Unified-7d-Reset":    strconv.FormatInt(shared.Unix(), 10),
		},
	}}
	if got := rankSoonestReset(auth, "claude-fable-5", now).weeklyReset; !got.Equal(shared) {
		t.Fatalf("weekly reset = %v, want shared 7d reset %v", got, shared)
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
		Quota: QuotaState{
			ObservedAt: now,
			Signals:    codexWindowSignals("Secondary", weeklyWindowMinutes, 0, now.Add(time.Hour)),
		},
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

// claudeAuthWithWindows observes a 5-hour window and a weekly window at 50% use.
func claudeAuthWithWindows(id string, fiveHourUtilization float64, fiveHourReset, weekReset time.Time) *Auth {
	auth := claudeAuthResettingAt(id, weekReset)
	auth.Quota.Signals["Anthropic-Ratelimit-Unified-7d-Utilization"] = "0.5"
	auth.Quota.Signals["Anthropic-Ratelimit-Unified-5h-Utilization"] = strconv.FormatFloat(fiveHourUtilization, 'f', -1, 64)
	auth.Quota.Signals["Anthropic-Ratelimit-Unified-5h-Reset"] = unixString(fiveHourReset)
	return auth
}

func codexWindowSignals(position string, minutes int, usedPercent float64, resetAt time.Time) map[string]string {
	prefix := "X-Codex-" + position + "-"
	return map[string]string{
		prefix + "Window-Minutes": strconv.Itoa(minutes),
		prefix + "Used-Percent":   strconv.FormatFloat(usedPercent, 'f', -1, 64),
		prefix + "Reset-At":       unixString(resetAt),
	}
}

func codexAuthWithSignals(id string, signalSets ...map[string]string) *Auth {
	signals := map[string]string{}
	for _, set := range signalSets {
		for key, value := range set {
			signals[key] = value
		}
	}
	return &Auth{ID: id, Provider: "codex", Quota: QuotaState{ObservedAt: time.Now(), Signals: signals}}
}

// codexWeeklyAuth observes a Codex account whose weekly window is the primary one.
func codexWeeklyAuth(id string, usedPercent float64, resetAt time.Time) *Auth {
	return codexAuthWithSignals(id, codexWindowSignals("Primary", weeklyWindowMinutes, usedPercent, resetAt))
}

// pickSoonestReset picks with the selector and checks the scheduler fast path agrees.
func pickSoonestReset(t *testing.T, provider string, auths ...*Auth) string {
	t.Helper()
	got, err := (&SoonestResetSelector{}).Pick(context.Background(), provider, "", cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	scheduled, errPick := newSchedulerForTest(&SoonestResetSelector{}, auths...).pickSingle(context.Background(), provider, "", cliproxyexecutor.Options{}, nil)
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
	rejected := claudeAuthWithWindows("a", 0.5, now.Add(time.Hour), now.Add(day))
	rejected.Quota.Signals["Anthropic-Ratelimit-Unified-5h-Status"] = "rejected"
	limitReached := codexAuthWithSignals("a",
		codexWindowSignals("Primary", 300, 90, now.Add(2*time.Hour)),
		codexWindowSignals("Secondary", weeklyWindowMinutes, 40, now.Add(day)),
		map[string]string{"X-Codex-Limit-Reached": "true"},
	)
	fable := claudeAuthResettingAt("a", now.Add(day))
	fable.ModelStates = map[string]*ModelState{"claude-fable-5": {Quota: QuotaState{ObservedAt: now, Signals: map[string]string{
		"Anthropic-Ratelimit-Unified-7d_oi-Utilization": "1",
		"Anthropic-Ratelimit-Unified-7d_oi-Reset":       unixString(now.Add(2 * day)),
	}}}}

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
			name:     "codex limit reached",
			provider: "codex",
			auths:    []*Auth{limitReached, codexWeeklyAuth("b", 10, now.Add(3*day))},
			want:     "b",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pickSoonestReset(t, tt.provider, tt.auths...); got != tt.want {
				t.Fatalf("Pick() = %q, want %q", got, tt.want)
			}
		})
	}

	// An exhausted Fable window gates only the Fable model.
	for model, want := range map[string]string{"claude-fable-5": "b", "claude-opus-4-7": "a"} {
		got, err := (&SoonestResetSelector{}).Pick(context.Background(), "claude", model, cliproxyexecutor.Options{}, []*Auth{fable, later()})
		if err != nil {
			t.Fatalf("Pick(%s) error = %v", model, err)
		}
		if got.ID != want {
			t.Fatalf("Pick(%s) = %q, want %q", model, got.ID, want)
		}
	}
}

// A Codex window without a Window-Minutes header still gates the credential when exhausted.
func TestSoonestResetSelectorPick_CodexWindowWithoutLengthStillGates(t *testing.T) {
	t.Parallel()

	now := time.Now()
	primary := codexWindowSignals("Primary", 0, 100, now.Add(time.Hour))
	delete(primary, "X-Codex-Primary-Window-Minutes")
	a := codexAuthWithSignals("a", primary, codexWindowSignals("Secondary", weeklyWindowMinutes, 40, now.Add(day)))
	if got := pickSoonestReset(t, "codex", a, codexWeeklyAuth("b", 10, now.Add(3*day))); got != "b" {
		t.Fatalf("Pick() = %q, want b", got)
	}
}
