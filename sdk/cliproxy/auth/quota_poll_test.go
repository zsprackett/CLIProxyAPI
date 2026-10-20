package auth

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

type quotaPollingExecutor struct {
	id      string
	headers http.Header
	err     error
	polled  chan PollTrigger
	// release, when set, holds PollQuota after it reported on polled.
	release chan struct{}
}

func (e *quotaPollingExecutor) Identifier() string { return e.id }
func (e *quotaPollingExecutor) Execute(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}
func (e *quotaPollingExecutor) ExecuteStream(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	return nil, nil
}
func (e *quotaPollingExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}
func (e *quotaPollingExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}
func (e *quotaPollingExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return auth, nil
}
func (e *quotaPollingExecutor) PollQuota(_ context.Context, auth *Auth, trigger PollTrigger) (QuotaObservation, error) {
	if e.polled != nil {
		e.polled <- trigger
	}
	if e.release != nil {
		<-e.release
	}
	if e.err != nil {
		return QuotaObservation{}, e.err
	}
	obs, ok := fromHeadersAdapter(auth.Provider, e.headers, time.Now())
	if !ok {
		return QuotaObservation{}, ErrQuotaNotPollable
	}
	return obs, nil
}

// fromHeadersAdapter turns the header form the test fixtures use into a poll
// observation, the way the P1 pollers did before they returned observations.
func fromHeadersAdapter(provider string, headers http.Header, now time.Time) (QuotaObservation, bool) {
	kind, ok := providerKindOf(provider)
	if !ok {
		return QuotaObservation{}, false
	}
	var obs QuotaObservation
	var section SectionID
	switch kind {
	case ProviderClaude:
		obs, ok = claudeHeadersObservationScoped(headers, "fable", now)
		section = SectionClaudeUsage
	case ProviderCodex:
		obs, ok = codexHeadersObservation(headers, "", now)
		section = SectionCodexUsage
	case ProviderXAI:
		return XAIBillingPollObservation(headers, now)
	default:
		return QuotaObservation{}, false
	}
	if !ok {
		return QuotaObservation{}, false
	}
	obs.Source = SourcePoll
	obs.Sections = map[SectionID]SectionStatus{section: SectionOK}
	for i := range obs.Windows {
		obs.Windows[i].Section = section
		obs.Windows[i].Source = SourcePoll
	}
	return obs, true
}

func TestManager_QuotaPollInterval(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	if got := manager.quotaPollInterval(); got != 0 {
		t.Fatalf("default interval = %s, want 0 (disabled)", got)
	}
	manager.SetConfig(&internalconfig.Config{QuotaPollIntervalSeconds: 300})
	if got := manager.quotaPollInterval(); got != 5*time.Minute {
		t.Fatalf("interval = %s, want 5m", got)
	}
	manager.SetConfig(&internalconfig.Config{QuotaPollIntervalSeconds: -1})
	if got := manager.quotaPollInterval(); got != 0 {
		t.Fatalf("negative interval = %s, want 0 (disabled)", got)
	}
}

func TestQuotaPollDue(t *testing.T) {
	last := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		now      time.Time
		last     time.Time
		interval time.Duration
		want     bool
	}{
		{"disabled", last.Add(time.Hour), last, 0, false},
		{"never polled", last, time.Time{}, 5 * time.Minute, true},
		{"before interval", last.Add(4 * time.Minute), last, 5 * time.Minute, false},
		{"at interval", last.Add(5 * time.Minute), last, 5 * time.Minute, true},
		{"one tick early is not due", last.Add(30 * time.Second), last, time.Minute, false},
		{"a late tick within half a tick is due", last.Add(59 * time.Second), last, time.Minute, true},
		{"a half tick early is due", last.Add(45 * time.Second), last, time.Minute, true},
		{"less than half a tick is not due", last.Add(44 * time.Second), last, time.Minute, false},
	}
	for _, tc := range cases {
		if got := quotaPollDue(tc.now, tc.last, tc.interval); got != tc.want {
			t.Errorf("%s: quotaPollDue = %t, want %t", tc.name, got, tc.want)
		}
	}
}

func TestManager_QuotaPollTargetsAndObservation(t *testing.T) {
	ctx := context.Background()
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.RegisterExecutor(&quotaPollingExecutor{
		id:      "codex",
		headers: http.Header{"X-Codex-Primary-Used-Percent": []string{"40"}},
	})
	// Antigravity does not support quota observation, so its executor is never polled.
	manager.RegisterExecutor(&quotaPollingExecutor{id: "antigravity"})

	nextRecover := time.Now().Add(time.Hour)
	for _, auth := range []*Auth{
		{ID: "codex-a", Provider: "codex", Quota: QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: nextRecover}},
		{ID: "codex-disabled", Provider: "codex", Disabled: true, Status: StatusDisabled},
		{ID: "ag-a", Provider: "antigravity"},
	} {
		if _, errRegister := manager.Register(ctx, auth); errRegister != nil {
			t.Fatalf("register %s: %v", auth.ID, errRegister)
		}
	}

	targets := manager.quotaPollTargets()
	if len(targets) != 1 || targets[0].auth.ID != "codex-a" {
		ids := make([]string, 0, len(targets))
		for _, target := range targets {
			ids = append(ids, target.auth.ID)
		}
		t.Fatalf("poll targets = %v, want [codex-a]", ids)
	}

	before, _ := manager.GetByID("codex-a")
	manager.pollQuotaOnce(ctx, targets[0], PollBackground)
	after, _ := manager.GetByID("codex-a")
	var primary *QuotaWindowObs
	for i := range after.QuotaSnapshot.Windows {
		if after.QuotaSnapshot.Windows[i].ID == "primary" {
			primary = &after.QuotaSnapshot.Windows[i]
		}
	}
	if primary == nil {
		t.Fatalf("polled snapshot has no primary window: %+v", after.QuotaSnapshot)
	}
	if used, known := primary.Used.Get(); !known || used != 40 || primary.Source != SourcePoll {
		t.Fatalf("polled primary window = %+v", *primary)
	}
	if after.QuotaSnapshot.UpdatedAt.IsZero() || after.QuotaSnapshot.LastPoll == nil {
		t.Fatalf("polled snapshot has no observation time or poll status: %+v", after.QuotaSnapshot)
	}
	if !after.Quota.Exceeded || after.Quota.Reason != "quota" || !after.Quota.NextRecoverAt.Equal(nextRecover) {
		t.Fatalf("polling changed cooldown state: %#v", after.Quota)
	}
	if after.Generation <= before.Generation {
		t.Fatalf("generation = %d, want > %d so the scheduler picks up the snapshot", after.Generation, before.Generation)
	}
}

// pollingHook runs before PollQuota returns, to simulate traffic landing mid-poll.
type pollingHook struct {
	quotaPollingExecutor
	before func()
}

func (e *pollingHook) PollQuota(ctx context.Context, auth *Auth, trigger PollTrigger) (QuotaObservation, error) {
	if e.before != nil {
		e.before()
	}
	return e.quotaPollingExecutor.PollQuota(ctx, auth, trigger)
}

func claudePollHeaders(fiveHour, sevenDay string) http.Header {
	h := http.Header{}
	if fiveHour != "" {
		h.Set("Anthropic-Ratelimit-Unified-5h-Utilization", fiveHour)
		h.Set("Anthropic-Ratelimit-Unified-5h-Reset", "1787296800")
	}
	if sevenDay != "" {
		h.Set("Anthropic-Ratelimit-Unified-7d-Utilization", sevenDay)
		h.Set("Anthropic-Ratelimit-Unified-7d-Reset", "1787296800")
	}
	return h
}

func codexPollHeaders(limitReached string) http.Header {
	h := http.Header{}
	h.Set("X-Codex-Primary-Used-Percent", "20")
	h.Set("X-Codex-Primary-Window-Minutes", "300")
	h.Set("X-Codex-Secondary-Used-Percent", "40")
	h.Set("X-Codex-Secondary-Window-Minutes", "10080")
	if limitReached != "" {
		h.Set("X-Codex-Limit-Reached", limitReached)
	}
	return h
}

// newBlockedPollManager registers a credential with sibling model states that
// carry a credential_quota cooldown, as MarkResult produces for a scoped 429.
func newBlockedPollManager(t *testing.T, provider string, exec ProviderExecutor, cfg *internalconfig.Config) *Manager {
	t.Helper()
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.RegisterExecutor(exec)
	if _, errRegister := manager.Register(WithSkipPersist(context.Background()), &Auth{
		ID:       provider + "-blocked",
		Provider: provider,
		Status:   StatusActive,
		ModelStates: map[string]*ModelState{
			"model-a": {Status: StatusActive},
			"model-b": {Status: StatusActive},
		},
	}); errRegister != nil {
		t.Fatalf("register: %v", errRegister)
	}
	markCredentialQuota(manager, provider+"-blocked", provider)
	// Stored after the cooldown exists: Home mode never records one itself, and
	// SetConfig would clear it.
	if cfg != nil {
		manager.runtimeConfig.Store(cfg)
	}
	return manager
}

func markCredentialQuota(manager *Manager, authID, provider string) {
	manager.MarkResult(context.Background(), Result{
		AuthID:          authID,
		Provider:        provider,
		Model:           "model-a",
		CredentialScope: true,
		Error:           &Error{Code: "quota_exceeded", Message: "429", HTTPStatus: http.StatusTooManyRequests},
	})
}

func pollOnce(t *testing.T, manager *Manager, authID string) {
	t.Helper()
	for _, target := range manager.quotaPollTargets() {
		if target.auth.ID == authID {
			manager.pollQuotaOnce(context.Background(), target, PollBackground)
			return
		}
	}
	t.Fatalf("no poll target for %s", authID)
}

func assertBlocked(t *testing.T, manager *Manager, authID string, want bool) {
	t.Helper()
	got, _ := manager.GetByID(authID)
	if got == nil {
		t.Fatalf("auth %s missing", authID)
	}
	if blocked := hasActiveQuotaCooldown(got, time.Now()); blocked != want {
		t.Fatalf("active quota cooldown = %t, want %t (auth %#v)", blocked, want, got.Quota)
	}
	if !want {
		if got.Unavailable {
			t.Fatal("auth still Unavailable after clear")
		}
		for model, state := range got.ModelStates {
			if state != nil && state.Quota.Exceeded {
				t.Fatalf("model %s still Quota.Exceeded", model)
			}
		}
	}
}

func TestQuotaPollClearsClaudeCooldownWhenCapacityShown(t *testing.T) {
	exec := &quotaPollingExecutor{id: "claude", headers: claudePollHeaders("0.3", "0.5")}
	manager := newBlockedPollManager(t, "claude", exec, nil)
	assertBlocked(t, manager, "claude-blocked", true)
	pollOnce(t, manager, "claude-blocked")
	assertBlocked(t, manager, "claude-blocked", false)
	// A second poll finds nothing to clear.
	pollOnce(t, manager, "claude-blocked")
	assertBlocked(t, manager, "claude-blocked", false)
}

func TestQuotaPollKeepsClaudeCooldownWhenWindowFull(t *testing.T) {
	for name, headers := range map[string]http.Header{
		"7d full":    claudePollHeaders("0.3", "1.0"),
		"7d missing": claudePollHeaders("0.3", ""),
	} {
		t.Run(name, func(t *testing.T) {
			exec := &quotaPollingExecutor{id: "claude", headers: headers}
			manager := newBlockedPollManager(t, "claude", exec, nil)
			pollOnce(t, manager, "claude-blocked")
			assertBlocked(t, manager, "claude-blocked", true)
		})
	}
}

func TestQuotaPollKeepsCooldownWhenFailureLandsMidPoll(t *testing.T) {
	exec := &pollingHook{quotaPollingExecutor: quotaPollingExecutor{id: "claude", headers: claudePollHeaders("0.3", "0.5")}}
	manager := newBlockedPollManager(t, "claude", exec, nil)
	exec.before = func() { markCredentialQuota(manager, "claude-blocked", "claude") }
	pollOnce(t, manager, "claude-blocked")
	assertBlocked(t, manager, "claude-blocked", true)
}

func TestQuotaPollLeavesNonQuotaBlockUntouched(t *testing.T) {
	exec := &quotaPollingExecutor{id: "claude", headers: claudePollHeaders("0.3", "0.5")}
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.RegisterExecutor(exec)
	next := time.Now().Add(time.Hour)
	if _, errRegister := manager.Register(WithSkipPersist(context.Background()), &Auth{
		ID:       "claude-401",
		Provider: "claude",
		Status:   StatusActive,
		ModelStates: map[string]*ModelState{
			"model-a": {Status: StatusError, Unavailable: true, NextRetryAfter: next},
		},
	}); errRegister != nil {
		t.Fatalf("register: %v", errRegister)
	}
	pollOnce(t, manager, "claude-401")
	got, _ := manager.GetByID("claude-401")
	state := got.ModelStates["model-a"]
	if !state.Unavailable || !state.NextRetryAfter.Equal(next) {
		t.Fatalf("401 block changed: %#v", state)
	}
}

func TestQuotaPollDoesNotClearWhenHomeEnabled(t *testing.T) {
	exec := &quotaPollingExecutor{id: "claude", headers: claudePollHeaders("0.3", "0.5")}
	cfg := &internalconfig.Config{}
	cfg.Home.Enabled = true
	manager := newBlockedPollManager(t, "claude", exec, cfg)
	pollOnce(t, manager, "claude-blocked")
	assertBlocked(t, manager, "claude-blocked", true)
}

func TestQuotaPollClearsCodexCooldown(t *testing.T) {
	for name, tc := range map[string]struct {
		limitReached string
		wantBlocked  bool
	}{
		"capacity":      {"", false},
		"limit reached": {"true", true},
	} {
		t.Run(name, func(t *testing.T) {
			exec := &quotaPollingExecutor{id: "codex", headers: codexPollHeaders(tc.limitReached)}
			manager := newBlockedPollManager(t, "codex", exec, nil)
			pollOnce(t, manager, "codex-blocked")
			assertBlocked(t, manager, "codex-blocked", tc.wantBlocked)
		})
	}
}

func TestQuotaPollDoesNotClearXAICooldown(t *testing.T) {
	h := http.Header{}
	h.Set("X-Xai-Billing-Used-Percent", "10")
	exec := &quotaPollingExecutor{id: "xai", headers: h}
	manager := newBlockedPollManager(t, "xai", exec, nil)
	pollOnce(t, manager, "xai-blocked")
	assertBlocked(t, manager, "xai-blocked", true)
}

func TestPollShowsQuotaCapacity(t *testing.T) {
	observe := func(provider string, h http.Header) QuotaObservation {
		obs, _ := fromHeadersAdapter(provider, h, time.Now())
		return obs
	}
	codexWith := func(key, value string) http.Header {
		h := codexPollHeaders("")
		h.Set(key, value)
		return h
	}
	cases := []struct {
		name     string
		provider string
		headers  http.Header
		want     bool
	}{
		{"claude both below", "claude", claudePollHeaders("0.3", "0.5"), true},
		{"claude 5h full", "claude", claudePollHeaders("1.0", "0.5"), false},
		{"claude 7d full", "claude", claudePollHeaders("0.3", "1.0"), false},
		{"claude 5h missing", "claude", claudePollHeaders("", "0.5"), false},
		{"claude 7d missing", "claude", claudePollHeaders("0.3", ""), false},
		{"claude no headers", "claude", http.Header{}, false},
		{"codex below", "codex", codexPollHeaders(""), true},
		{"codex limit reached", "codex", codexPollHeaders("true"), false},
		{"codex limit reached mixed case", "codex", codexWith("X-Codex-Limit-Reached", "True"), false},
		{"codex limit not reached", "codex", codexWith("X-Codex-Limit-Reached", "false"), true},
		{"codex not allowed", "codex", codexWith("X-Codex-Allowed", "false"), false},
		{"codex allowed", "codex", codexWith("X-Codex-Allowed", "true"), true},
		{"codex window full", "codex", codexWith("X-Codex-Secondary-Used-Percent", "100"), false},
		{"codex no windows", "codex", http.Header{"X-Codex-Primary-Used-Percent": []string{"5"}}, false},
		{"xai", "xai", http.Header{"X-Xai-Billing-Used-Percent": []string{"10"}}, false},
		{"unknown provider", "gemini", codexPollHeaders(""), false},
	}
	for _, tc := range cases {
		if got := pollShowsQuotaCapacity(observe(tc.provider, tc.headers)); got != tc.want {
			t.Errorf("%s: pollShowsQuotaCapacity = %t, want %t", tc.name, got, tc.want)
		}
	}
}

func TestHasActiveQuotaCooldown(t *testing.T) {
	now := time.Now()
	future, past := now.Add(time.Hour), now.Add(-time.Hour)
	quota := func(reason string, next time.Time) QuotaState {
		return QuotaState{Exceeded: true, Reason: reason, NextRecoverAt: next}
	}
	cases := []struct {
		name string
		auth *Auth
		want bool
	}{
		{"nil", nil, false},
		{"auth credential_quota", &Auth{Quota: quota("credential_quota", future)}, true},
		{"auth quota", &Auth{Quota: quota("quota", future)}, true},
		{"expired", &Auth{Quota: quota("quota", past)}, false},
		{"cloudflare", &Auth{Quota: quota("cloudflare", future)}, false},
		{"not exceeded", &Auth{Quota: QuotaState{Reason: "quota", NextRecoverAt: future}}, false},
		{"model state", &Auth{ModelStates: map[string]*ModelState{"nil": nil, "m": {Quota: quota("quota", future)}}}, true},
		{"model state cloudflare", &Auth{ModelStates: map[string]*ModelState{"m": {Quota: quota("cloudflare", future)}}}, false},
	}
	for _, tc := range cases {
		if got := hasActiveQuotaCooldown(tc.auth, now); got != tc.want {
			t.Errorf("%s: hasActiveQuotaCooldown = %t, want %t", tc.name, got, tc.want)
		}
	}
}

func TestQuotaPollKeepsClaudeFableWindowForRouting(t *testing.T) {
	now := time.Now()
	obs, ok := fromHeadersAdapter("claude", quotaTestHeaders(
		"Anthropic-Ratelimit-Unified-5h-Utilization", "0.2",
		"Anthropic-Ratelimit-Unified-7d-Utilization", "0.4",
		"Anthropic-Ratelimit-Unified-7d_oi-Utilization", "0.6",
		"Anthropic-Ratelimit-Unified-7d_oi-Reset", strconv.FormatInt(now.Add(time.Hour).Unix(), 10),
	), now)
	if !ok {
		t.Fatal("claude poll headers produced no observation")
	}
	var snapshot QuotaSnapshot
	snapshot.Observe(obs)
	got := windowsByClass(QuotaWindowsForAuth(&Auth{Provider: "claude", QuotaSnapshot: snapshot}))
	fable, ok := got["claude:7d_oi"]
	if !ok {
		t.Fatalf("polled 7d_oi window missing from routing windows: %+v", got)
	}
	if fable.ModelScope != "fable" || fable.UsedPercent != 60 {
		t.Fatalf("claude:7d_oi = %+v, want scope fable at 60", fable)
	}
}

func TestQuotaPollXAIPeriodSwitchRetiresWeeklyWindow(t *testing.T) {
	now := time.Now()
	var snapshot QuotaSnapshot
	weekly, ok := fromHeadersAdapter("xai", quotaTestHeaders(
		"X-Xai-Billing-Period-Type", "weekly", "X-Xai-Billing-Used-Percent", "40"), now.Add(-time.Hour))
	if !ok || !snapshot.Observe(weekly) {
		t.Fatal("weekly xai poll produced no change")
	}
	monthly, ok := fromHeadersAdapter("xai", quotaTestHeaders(
		"X-Xai-Billing-Period-Type", "monthly", "X-Xai-Billing-Used-Percent", "12"), now)
	if !ok || !snapshot.Observe(monthly) {
		t.Fatal("monthly xai poll produced no change")
	}
	got := windowsByClass(QuotaWindowsForAuth(&Auth{Provider: "xai", QuotaSnapshot: snapshot}))
	if stale, ok := got["xai:weekly"]; ok {
		t.Fatalf("stale weekly window survived the switch to a monthly period: %+v", stale)
	}
	if got["xai:monthly"].UsedPercent != 12 {
		t.Fatalf("monthly window = %+v, want 12 used", got["xai:monthly"])
	}
}

func TestQuotaPollXAIWeeklyAfterMonthlyRetiresMonthlyWindow(t *testing.T) {
	now := time.Now()
	var snapshot QuotaSnapshot
	monthly, ok := fromHeadersAdapter("xai", quotaTestHeaders(
		"X-Xai-Billing-Period-Type", "monthly", "X-Xai-Billing-Used-Percent", "12",
		"X-Xai-Billing-Window-Minutes", "43200",
		"X-Xai-Billing-Reset-At", now.Add(500*time.Hour).UTC().Format(time.RFC3339)), now.Add(-time.Hour))
	if !ok || !snapshot.Observe(monthly) {
		t.Fatal("monthly xai poll produced no change")
	}
	weekly, ok := fromHeadersAdapter("xai", quotaTestHeaders(
		"X-Xai-Billing-Period-Type", "weekly", "X-Xai-Billing-Used-Percent", "35",
		"X-Xai-Billing-Reset-At", now.Add(100*time.Hour).UTC().Format(time.RFC3339)), now)
	if !ok || !snapshot.Observe(weekly) {
		t.Fatal("weekly xai poll produced no change")
	}
	got := windowsByClass(QuotaWindowsForAuth(&Auth{Provider: "xai", QuotaSnapshot: snapshot}))
	if stale, ok := got["xai:monthly"]; ok {
		t.Fatalf("stale monthly window survived the switch to a weekly period: %+v", stale)
	}
	if got["xai:weekly"].UsedPercent != 35 {
		t.Fatalf("weekly window = %+v, want 35 used", got["xai:weekly"])
	}
}

// fakeClock is a clock the test sets; the poll goroutines read it.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Set(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = now
}

// newTickPollManager registers one Claude credential with a 60 s poll
// interval and a clock the test drives.
func newTickPollManager(t *testing.T, exec *quotaPollingExecutor, clock *fakeClock) *Manager {
	t.Helper()
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.RegisterExecutor(exec)
	manager.quotaPoll.nowFunc = clock.Now
	cfg := &internalconfig.Config{QuotaPollIntervalSeconds: 60}
	manager.runtimeConfig.Store(cfg)
	if _, errRegister := manager.Register(WithSkipPersist(context.Background()), &Auth{ID: "claude-a", Provider: "claude", Status: StatusActive}); errRegister != nil {
		t.Fatalf("register: %v", errRegister)
	}
	return manager
}

func awaitPoll(t *testing.T, polled <-chan PollTrigger, want PollTrigger) {
	t.Helper()
	select {
	case got := <-polled:
		if got != want {
			t.Fatalf("poll trigger = %v, want %v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no poll ran")
	}
}

// awaitIdle waits for the in-flight poll of authID, if any, to finish.
func awaitIdle(manager *Manager, authID string) {
	manager.quotaPoll.mu.Lock()
	call := manager.quotaPoll.inFlight[authID]
	manager.quotaPoll.mu.Unlock()
	if call != nil {
		<-call.done
	}
}

func assertNoPoll(t *testing.T, polled <-chan PollTrigger) {
	t.Helper()
	select {
	case got := <-polled:
		t.Fatalf("unexpected %v poll", got)
	default:
	}
}

// The loop wakes every 30 s. A 60 s interval must poll every 60 s, not every
// 90 s, and a click resets the background timer.
func TestRunQuotaPollTicksMatchesConfiguredInterval(t *testing.T) {
	start := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	clock := &fakeClock{now: start}
	exec := &quotaPollingExecutor{id: "claude", headers: claudePollHeaders("0.3", "0.5"), polled: make(chan PollTrigger)}
	manager := newTickPollManager(t, exec, clock)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan time.Time)
	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.runQuotaPollTicks(ctx, ticks)
	}()
	tick := func(offset time.Duration) {
		awaitIdle(manager, "claude-a")
		clock.Set(start.Add(offset))
		ticks <- clock.Now()
	}

	tick(0)
	awaitPoll(t, exec.polled, PollBackground)
	tick(30 * time.Second)
	tick(60 * time.Second)
	awaitPoll(t, exec.polled, PollBackground)
	tick(90 * time.Second)
	tick(120 * time.Second)
	awaitPoll(t, exec.polled, PollBackground)

	// A click at second 150 polls now and defers the next background poll to second 210.
	awaitIdle(manager, "claude-a")
	clock.Set(start.Add(150 * time.Second))
	go func() {
		if _, errPoll := manager.PollQuotaNow(ctx, "claude-a"); errPoll != nil {
			t.Errorf("PollQuotaNow: %v", errPoll)
		}
	}()
	awaitPoll(t, exec.polled, PollOnDemand)
	tick(180 * time.Second)
	tick(210 * time.Second)
	awaitPoll(t, exec.polled, PollBackground)
	awaitIdle(manager, "claude-a")
	assertNoPoll(t, exec.polled)
	cancel()
	<-done
}

// Two on-demand polls of one credential share one upstream request.
func TestPollQuotaNowJoinsInFlightPoll(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	exec := &quotaPollingExecutor{id: "claude", headers: claudePollHeaders("0.3", "0.5"), polled: make(chan PollTrigger), release: make(chan struct{})}
	manager := newTickPollManager(t, exec, clock)
	joined := make(chan struct{})
	manager.quotaPoll.joined = func() { joined <- struct{}{} }
	ctx := context.Background()
	results := make(chan QuotaSnapshot, 2)
	pollNow := func() {
		snapshot, errPoll := manager.PollQuotaNow(ctx, "claude-a")
		if errPoll != nil {
			t.Errorf("PollQuotaNow: %v", errPoll)
		}
		results <- snapshot
	}
	go pollNow()
	awaitPoll(t, exec.polled, PollOnDemand)
	// The first poll is held inside PollQuota, so the second call must join it.
	go pollNow()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("second call did not join the poll in flight")
	}
	exec.release <- struct{}{}
	first, second := <-results, <-results
	assertNoPoll(t, exec.polled)
	if first.LastPoll == nil || second.LastPoll == nil || !first.LastPoll.At.Equal(second.LastPoll.At) {
		t.Fatalf("joined polls differ: %+v vs %+v", first.LastPoll, second.LastPoll)
	}
	if first.LastPoll.Trigger != PollOnDemand {
		t.Fatalf("trigger = %v, want on_demand", first.LastPoll.Trigger)
	}
}

func TestPollQuotaNowErrors(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)}
	exec := &quotaPollingExecutor{id: "claude", headers: claudePollHeaders("0.3", "0.5")}
	manager := newTickPollManager(t, exec, clock)
	ctx := context.Background()
	if _, errPoll := manager.PollQuotaNow(ctx, "missing"); !errors.Is(errPoll, ErrQuotaAuthNotFound) {
		t.Fatalf("unknown auth error = %v, want ErrQuotaAuthNotFound", errPoll)
	}
	if _, errRegister := manager.Register(WithSkipPersist(ctx), &Auth{ID: "ag-a", Provider: "antigravity", Status: StatusActive}); errRegister != nil {
		t.Fatalf("register: %v", errRegister)
	}
	if _, errPoll := manager.PollQuotaNow(ctx, "ag-a"); !errors.Is(errPoll, ErrQuotaNotPollable) {
		t.Fatalf("unsupported provider error = %v, want ErrQuotaNotPollable", errPoll)
	}
	exec.err = ErrQuotaNotPollable
	if _, errPoll := manager.PollQuotaNow(ctx, "claude-a"); !errors.Is(errPoll, ErrQuotaNotPollable) {
		t.Fatalf("api key error = %v, want ErrQuotaNotPollable", errPoll)
	}
	got, _ := manager.GetByID("claude-a")
	if !got.QuotaSnapshot.IsZero() {
		t.Fatalf("a not-pollable credential gained a snapshot: %+v", got.QuotaSnapshot)
	}

	exec.err = errors.New("usage: 503")
	snapshot, errPoll := manager.PollQuotaNow(ctx, "claude-a")
	if errPoll != nil {
		t.Fatalf("upstream failure returned an error: %v", errPoll)
	}
	if snapshot.LastPoll == nil || snapshot.LastPoll.Error != "usage: 503" || !snapshot.LastPoll.At.Equal(clock.Now()) {
		t.Fatalf("last poll = %+v, want the upstream error at the clock time", snapshot.LastPoll)
	}

	cfg := &internalconfig.Config{QuotaPollIntervalSeconds: 60}
	cfg.Home.Enabled = true
	manager.runtimeConfig.Store(cfg)
	if _, errPoll := manager.PollQuotaNow(ctx, "claude-a"); !errors.Is(errPoll, ErrQuotaPollHomeMode) {
		t.Fatalf("home mode error = %v, want ErrQuotaPollHomeMode", errPoll)
	}
}
