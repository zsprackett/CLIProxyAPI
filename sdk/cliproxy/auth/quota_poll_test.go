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
