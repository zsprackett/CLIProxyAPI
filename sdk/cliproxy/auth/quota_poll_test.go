package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

type quotaPollingExecutor struct {
	id      string
	headers http.Header
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
func (e *quotaPollingExecutor) PollQuota(context.Context, *Auth) (http.Header, error) {
	return e.headers.Clone(), nil
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
	manager.pollQuotaOnce(ctx, targets[0])
	after, _ := manager.GetByID("codex-a")
	if got := after.Quota.Signals["X-Codex-Primary-Used-Percent"]; got != "40" {
		t.Fatalf("polled signal = %q, want 40 (signals %#v)", got, after.Quota.Signals)
	}
	if after.Quota.ObservedAt.IsZero() {
		t.Fatal("polled snapshot has no observation time")
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

func (e *pollingHook) PollQuota(ctx context.Context, auth *Auth) (http.Header, error) {
	if e.before != nil {
		e.before()
	}
	return e.quotaPollingExecutor.PollQuota(ctx, auth)
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
			manager.pollQuotaOnce(context.Background(), target)
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
	observe := func(provider string, h http.Header) QuotaState {
		var q QuotaState
		q.ObserveResponseHeadersForProvider(provider, h, time.Now())
		return q
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
		if got := pollShowsQuotaCapacity(tc.provider, observe(tc.provider, tc.headers)); got != tc.want {
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
