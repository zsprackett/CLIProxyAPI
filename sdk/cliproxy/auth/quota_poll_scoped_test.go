package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

const (
	scopedFableModel = "claude-fable-5"
	scopedOpusModel  = "claude-opus-4-7"
)

// observationPoller returns a fixed Claude poll observation.
type observationPoller struct {
	quotaPollingExecutor
	obs QuotaObservation
}

func (e *observationPoller) PollQuota(context.Context, *Auth, PollTrigger) (QuotaObservation, error) {
	return e.obs, nil
}

type claudePollSpec struct {
	fiveHour, sevenDay float64
	// fable is nil when the poll has no Fable window.
	fable *float64
	// extra is nil when the poll has no extra_usage block.
	extra *ClaudeExtraUsage
}

func percentPtr(v float64) *float64 { return &v }

func claudeScopedPollObservation(spec claudePollSpec, now time.Time) QuotaObservation {
	window := func(id WindowID, scope string, used float64) QuotaWindowObs {
		return QuotaWindowObs{
			ID:         id,
			ModelScope: scope,
			Used:       KnownPercent(used),
			ResetAt:    now.Add(3 * time.Hour),
			Period:     7 * 24 * time.Hour,
			Section:    SectionClaudeUsage,
			Source:     SourcePoll,
			ObservedAt: now,
		}
	}
	obs := QuotaObservation{
		Provider:   ProviderClaude,
		Source:     SourcePoll,
		ObservedAt: now,
		Sections:   map[SectionID]SectionStatus{SectionClaudeUsage: SectionOK},
		Windows: []QuotaWindowObs{
			window("five_hour", "", spec.fiveHour),
			window("seven_day", "", spec.sevenDay),
		},
	}
	if spec.fable != nil {
		obs.Windows = append(obs.Windows, window("seven_day_fable", "fable", *spec.fable))
	}
	extras := ClaudeExtras{}
	if spec.extra != nil {
		extras.ExtraUsage = OKSection(*spec.extra, now)
	}
	obs.Extras = extras
	return obs
}

// fableOnlyRejection is a Fable 429 with both account windows allowed.
func fableOnlyRejection() http.Header {
	return quotaTestHeaders(
		"Anthropic-Ratelimit-Unified-Status", "rejected",
		"Anthropic-Ratelimit-Unified-5h-Status", "allowed",
		"Anthropic-Ratelimit-Unified-5h-Utilization", "0.2",
		"Anthropic-Ratelimit-Unified-7d-Status", "allowed",
		"Anthropic-Ratelimit-Unified-7d-Utilization", "0.4",
		"Anthropic-Ratelimit-Unified-7d_oi-Status", "rejected",
		"Anthropic-Ratelimit-Unified-7d_oi-Utilization", "1.0",
		"Anthropic-Ratelimit-Unified-Representative-Claim", "seven_day_oi",
	)
}

// overageRejection is an overage-only 429 with both account windows allowed.
func overageRejection() http.Header {
	return quotaTestHeaders(
		"Anthropic-Ratelimit-Unified-Status", "rejected",
		"Anthropic-Ratelimit-Unified-5h-Status", "allowed",
		"Anthropic-Ratelimit-Unified-5h-Utilization", "0.2",
		"Anthropic-Ratelimit-Unified-7d-Status", "allowed",
		"Anthropic-Ratelimit-Unified-7d-Utilization", "0.4",
		"Anthropic-Ratelimit-Unified-Overage-Status", "rejected",
		"Anthropic-Ratelimit-Unified-Representative-Claim", "overage",
	)
}

func newScopedPollManager(t *testing.T, spec claudePollSpec) (*Manager, string) {
	t.Helper()
	authID := "claude-scoped-" + t.Name()
	exec := &observationPoller{
		quotaPollingExecutor: quotaPollingExecutor{id: "claude"},
		obs:                  claudeScopedPollObservation(spec, time.Now()),
	}
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	manager.RegisterExecutor(exec)
	registry.GetGlobalRegistry().RegisterClient(authID, "claude", []*registry.ModelInfo{{ID: scopedFableModel}, {ID: scopedOpusModel}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(authID) })
	if _, errRegister := manager.Register(WithSkipPersist(context.Background()), &Auth{
		ID:       authID,
		Provider: "claude",
		Status:   StatusActive,
	}); errRegister != nil {
		t.Fatalf("register: %v", errRegister)
	}
	return manager, authID
}

// markModelRateLimit records a model-scoped 429, as the Claude executor
// reports a Fable-only or overage rejection.
func markModelRateLimit(manager *Manager, authID, model string, headers http.Header) {
	ctx := internallogging.WithResponseHeadersHolder(context.Background())
	if headers != nil {
		internallogging.SetResponseHeaders(ctx, headers)
	}
	retryAfter := time.Hour
	manager.MarkResult(ctx, Result{
		AuthID:     authID,
		Provider:   "claude",
		Model:      model,
		RetryAfter: &retryAfter,
		Error:      &Error{Code: "rate_limit_error", Message: "429", HTTPStatus: http.StatusTooManyRequests},
	})
}

// assertModelRoutable sends a request for model and checks whether the manager
// refuses it with a local cooldown or lets it through to the executor.
func assertModelRoutable(t *testing.T, manager *Manager, model string, want bool) {
	t.Helper()
	_, errExecute := manager.Execute(context.Background(), []string{"claude"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
	var cooldownErr *modelCooldownError
	cooled := errors.As(errExecute, &cooldownErr)
	if want && errExecute != nil {
		t.Fatalf("%s request error = %v, want it routed after the poll", model, errExecute)
	}
	if !want && !cooled {
		t.Fatalf("%s request error = %v, want a local cooldown refusal", model, errExecute)
	}
}

func TestQuotaPollKeepsFableCooldownWhileFableWindowFull(t *testing.T) {
	manager, authID := newScopedPollManager(t, claudePollSpec{fiveHour: 20, sevenDay: 40, fable: percentPtr(100)})
	markModelRateLimit(manager, authID, scopedFableModel, fableOnlyRejection())
	assertModelRoutable(t, manager, scopedFableModel, false)
	pollOnce(t, manager, authID)
	assertModelRoutable(t, manager, scopedFableModel, false)
}

func TestQuotaPollKeepsFableCooldownWhenPollOmitsFableWindow(t *testing.T) {
	manager, authID := newScopedPollManager(t, claudePollSpec{fiveHour: 20, sevenDay: 40})
	markModelRateLimit(manager, authID, scopedFableModel, fableOnlyRejection())
	before, _ := manager.GetByID(authID)
	if fable, ok := windowsByClass(QuotaWindowsForAuth(before))["claude:7d_oi"]; !ok || fable.UsedPercent != 100 {
		t.Fatalf("snapshot Fable window before the poll = %+v (found %t), want 100", fable, ok)
	}
	pollOnce(t, manager, authID)
	assertModelRoutable(t, manager, scopedFableModel, false)
}

func TestQuotaPollClearsFableCooldownWhenFableWindowHasRoom(t *testing.T) {
	manager, authID := newScopedPollManager(t, claudePollSpec{fiveHour: 20, sevenDay: 40, fable: percentPtr(60)})
	markModelRateLimit(manager, authID, scopedFableModel, fableOnlyRejection())
	assertModelRoutable(t, manager, scopedFableModel, false)
	pollOnce(t, manager, authID)
	assertModelRoutable(t, manager, scopedFableModel, true)
}

func TestQuotaPollOverageCooldownNeedsOverageRoom(t *testing.T) {
	for name, tc := range map[string]struct {
		extra *ClaudeExtraUsage
		want  bool
	}{
		"no extra_usage block": {extra: nil, want: false},
		"extra usage disabled": {extra: &ClaudeExtraUsage{Enabled: false, Utilization: KnownPercent(10)}, want: false},
		"extra usage full":     {extra: &ClaudeExtraUsage{Enabled: true, Utilization: KnownPercent(100)}, want: false},
		"extra usage unknown":  {extra: &ClaudeExtraUsage{Enabled: true}, want: false},
		"extra usage has room": {extra: &ClaudeExtraUsage{Enabled: true, Utilization: KnownPercent(50)}, want: true},
	} {
		t.Run(name, func(t *testing.T) {
			manager, authID := newScopedPollManager(t, claudePollSpec{fiveHour: 20, sevenDay: 40, fable: percentPtr(10), extra: tc.extra})
			markModelRateLimit(manager, authID, scopedOpusModel, overageRejection())
			assertModelRoutable(t, manager, scopedOpusModel, false)
			pollOnce(t, manager, authID)
			assertModelRoutable(t, manager, scopedOpusModel, tc.want)
		})
	}
}

func TestQuotaPollClearsCredentialCooldownButKeepsFableCooldown(t *testing.T) {
	manager, authID := newScopedPollManager(t, claudePollSpec{fiveHour: 20, sevenDay: 40, fable: percentPtr(100)})
	markModelRateLimit(manager, authID, scopedFableModel, fableOnlyRejection())
	markCredentialQuota(manager, authID, "claude")
	assertModelRoutable(t, manager, scopedOpusModel, false)
	pollOnce(t, manager, authID)
	assertModelRoutable(t, manager, scopedOpusModel, true)
	assertModelRoutable(t, manager, scopedFableModel, false)
}

func TestQuotaPollClearsOpusCooldownButKeepsFableCooldown(t *testing.T) {
	manager, authID := newScopedPollManager(t, claudePollSpec{fiveHour: 20, sevenDay: 40, fable: percentPtr(100)})
	markModelRateLimit(manager, authID, scopedFableModel, fableOnlyRejection())
	markModelRateLimit(manager, authID, scopedOpusModel, nil)
	assertModelRoutable(t, manager, scopedOpusModel, false)
	pollOnce(t, manager, authID)
	assertModelRoutable(t, manager, scopedOpusModel, true)
	assertModelRoutable(t, manager, scopedFableModel, false)
}
