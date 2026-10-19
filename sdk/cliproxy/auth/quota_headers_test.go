package auth

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
)

func headersTestWindow(t *testing.T, snapshot QuotaSnapshot, id WindowID) QuotaWindowObs {
	t.Helper()
	for _, w := range snapshot.Windows {
		if w.ID == id {
			return w
		}
	}
	t.Fatalf("snapshot has no window %q: %+v", id, snapshot.Windows)
	return QuotaWindowObs{}
}

func headersTestAssertUsed(t *testing.T, w QuotaWindowObs, want float64) {
	t.Helper()
	used, known := w.Used.Get()
	if !known || used != want {
		t.Fatalf("window %q used = %v (known %t), want %v", w.ID, used, known, want)
	}
}

func headersTestWindowIDs(snapshot QuotaSnapshot) []WindowID {
	ids := make([]WindowID, 0, len(snapshot.Windows))
	for _, w := range snapshot.Windows {
		ids = append(ids, w.ID)
	}
	return ids
}

func TestClaudeHeadersProduceAccountWindowsAndScopedFableWindow(t *testing.T) {
	now := time.Unix(1787279282, 0)
	headers := http.Header{
		"Anthropic-Ratelimit-Unified-5h-Status":               []string{"allowed"},
		"Anthropic-Ratelimit-Unified-5h-Utilization":          []string{"0.0"},
		"Anthropic-Ratelimit-Unified-5h-Reset":                []string{"1787296800"},
		"Anthropic-Ratelimit-Unified-7d-Status":               []string{"allowed"},
		"Anthropic-Ratelimit-Unified-7d-Utilization":          []string{"0.53"},
		"Anthropic-Ratelimit-Unified-7d-Reset":                []string{"1787695200"},
		"Anthropic-Ratelimit-Unified-7d_oi-Utilization":       []string{"0.2"},
		"Anthropic-Ratelimit-Unified-7d_oi-Reset":             []string{"1787600000"},
		"Anthropic-Ratelimit-Unified-Overage-Disabled-Reason": []string{"member_zero_credit_limit"},
		"Anthropic-Workspace-Id":                              []string{"workspace-must-not-be-observed"},
	}

	var fable QuotaSnapshot
	if !fable.ObserveResponseHeaders("claude", "claude-fable-5", headers, now) {
		t.Fatal("fable observation reported no change")
	}
	if got, want := headersTestWindowIDs(fable), []WindowID{"five_hour", "seven_day", "seven_day_fable"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fable windows = %v, want %v", got, want)
	}
	five := headersTestWindow(t, fable, "five_hour")
	headersTestAssertUsed(t, five, 0)
	if five.ResetAt.Unix() != 1787296800 || five.Period != 5*time.Hour || five.ModelScope != "" {
		t.Fatalf("five_hour = %+v", five)
	}
	seven := headersTestWindow(t, fable, "seven_day")
	headersTestAssertUsed(t, seven, 53)
	if seven.Period != 7*24*time.Hour || seven.ModelScope != "" {
		t.Fatalf("seven_day = %+v", seven)
	}
	scoped := headersTestWindow(t, fable, "seven_day_fable")
	headersTestAssertUsed(t, scoped, 20)
	if scoped.ModelScope != "fable" || scoped.Class(ProviderClaude) != "claude:7d_oi" {
		t.Fatalf("seven_day_fable = %+v", scoped)
	}

	var other QuotaSnapshot
	if !other.ObserveResponseHeaders("claude", "claude-opus-4-6", headers, now) {
		t.Fatal("non-fable observation reported no change")
	}
	if got, want := headersTestWindowIDs(other), []WindowID{"five_hour", "seven_day"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("non-fable windows = %v, want %v (7d_oi dropped)", got, want)
	}
}

func TestCodexHeadersProduceWindowsByPeriodAndNamespace(t *testing.T) {
	now := time.Unix(1787279282, 0)

	var main QuotaSnapshot
	if !main.ObserveResponseHeaders("codex", "gpt-5.3-codex", http.Header{
		"X-Codex-Plan-Type":                          []string{"pro"},
		"X-Codex-Primary-Used-Percent":               []string{"51"},
		"X-Codex-Primary-Window-Minutes":             []string{"10080"},
		"X-Codex-Primary-Reset-After-Seconds":        []string{"309718"},
		"X-Codex-Primary-Reset-At":                   []string{"1787588999"},
		"X-Codex-Secondary-Used-Percent":             []string{"7"},
		"X-Codex-Secondary-Window-Minutes":           []string{"300"},
		"X-Codex-Code-Review-Primary-Used-Percent":   []string{"3"},
		"X-Codex-Code-Review-Secondary-Used-Percent": []string{"4"},
		"X-Codex-Credits-Has-Credits":                []string{"False"},
		"X-Codex-Turn-State":                         []string{"opaque-state"},
		"Authorization":                              []string{"Bearer secret"},
	}, now) {
		t.Fatal("Codex observation reported no change")
	}
	primary := headersTestWindow(t, main, "primary")
	headersTestAssertUsed(t, primary, 51)
	if primary.Class(ProviderCodex) != "codex:weekly" || primary.ResetAt.Unix() != 1787588999 {
		t.Fatalf("primary = %+v", primary)
	}
	secondary := headersTestWindow(t, main, "secondary")
	headersTestAssertUsed(t, secondary, 7)
	if secondary.Class(ProviderCodex) != "codex:5h" {
		t.Fatalf("secondary class = %q, want codex:5h", secondary.Class(ProviderCodex))
	}
	for _, id := range []WindowID{"code_review.primary", "code_review.secondary"} {
		if class := headersTestWindow(t, main, id).Class(ProviderCodex); class != "" {
			t.Fatalf("%s class = %q, want display-only", id, class)
		}
	}
	extras, ok := main.Extras.(CodexExtras)
	if !ok || extras.Plan.Value == nil || *extras.Plan.Value != "pro" {
		t.Fatalf("plan extras = %+v", main.Extras)
	}
	if extras.Credits.Value == nil || extras.Credits.Value.HasCredits == nil || *extras.Credits.Value.HasCredits {
		t.Fatalf("credits extras = %+v", extras.Credits)
	}

	httpForm := http.Header{
		"X-Codex-Bengalfox-Limit-Name":             []string{"GPT-5.3-Codex-Spark"},
		"X-Codex-Bengalfox-Primary-Used-Percent":   []string{"12"},
		"X-Codex-Bengalfox-Secondary-Used-Percent": []string{"35"},
	}
	websocket := http.Header{}
	websocket.Set("X-Codex-Additional-GPT-5.3-Codex-Spark-Limit-Name", "GPT-5.3-Codex-Spark")
	websocket.Set("X-Codex-Additional-GPT-5.3-Codex-Spark-Primary-Used-Percent", "12")
	for name, headers := range map[string]http.Header{"http": httpForm, "websocket": websocket} {
		var snapshot QuotaSnapshot
		if !snapshot.ObserveResponseHeaders("codex", "gpt-5.3-codex-spark", headers, now) {
			t.Fatalf("%s observation reported no change", name)
		}
		window := headersTestWindow(t, snapshot, "additional.GPT-5.3-Codex-Spark.primary")
		headersTestAssertUsed(t, window, 12)
		if window.Label != "GPT-5.3-Codex-Spark" || window.Class(ProviderCodex) != "" {
			t.Fatalf("%s additional window = %+v", name, window)
		}
	}
	var httpSnapshot QuotaSnapshot
	httpSnapshot.ObserveResponseHeaders("codex", "", httpForm, now)
	headersTestAssertUsed(t, headersTestWindow(t, httpSnapshot, "additional.GPT-5.3-Codex-Spark.secondary"), 35)
}

func TestQuotaHeaderValueRejectsControlCharactersAndOversizeValues(t *testing.T) {
	cases := map[string]string{
		"control character": "evil\r\nX-Injected: 1",
		"delete character":  "pro\x7f",
		"oversize":          strings.Repeat("x", maxQuotaSignalValue+1),
	}
	for name, value := range cases {
		if got := quotaHeaderValue(http.Header{"X-Codex-Plan-Type": []string{value}}, "X-Codex-Plan-Type"); got != "" {
			t.Fatalf("%s: quotaHeaderValue = %q, want empty", name, got)
		}
		var snapshot QuotaSnapshot
		if snapshot.ObserveResponseHeaders("codex", "", http.Header{"X-Codex-Plan-Type": []string{value}}, time.Unix(100, 0)) {
			t.Fatalf("%s: value was observed", name)
		}
		if !snapshot.IsZero() {
			t.Fatalf("%s: value was stored: %+v", name, snapshot)
		}
	}
	if got := quotaHeaderValue(http.Header{"X-Codex-Plan-Type": []string{strings.Repeat("x", maxQuotaSignalValue)}}, "X-Codex-Plan-Type"); len(got) != maxQuotaSignalValue {
		t.Fatalf("value at the limit was rejected: len %d", len(got))
	}
}

func TestProviderWithoutPassiveQuotaHeadersProducesNoObservation(t *testing.T) {
	headers := http.Header{
		"X-Ratelimit-Remaining-Requests":             []string{"0"},
		"X-Ratelimit-Remaining-Tokens":               []string{"0"},
		"Retry-After":                                []string{"60"},
		"Anthropic-Ratelimit-Unified-5h-Utilization": []string{"0.5"},
		"X-Codex-Primary-Used-Percent":               []string{"50"},
	}
	for _, provider := range []string{"kimi", "grok", "antigravity", "gemini", "vertex", "aistudio"} {
		if ProviderSupportsQuotaObservation(provider) {
			t.Fatalf("provider %s unexpectedly supports quota observation", provider)
		}
		var snapshot QuotaSnapshot
		if snapshot.ObserveResponseHeaders(provider, "", headers, time.Now()) {
			t.Fatalf("provider %s reported an observation", provider)
		}
		if !snapshot.IsZero() {
			t.Fatalf("provider %s stored a snapshot: %+v", provider, snapshot)
		}
	}
}

func TestProviderSupportsQuotaObservation(t *testing.T) {
	for _, provider := range []string{
		"", "kimi", "grok", "antigravity", "gemini", "gemini-interactions",
		"vertex", "aistudio", "openai", "openai-compatibility", "third-party-plugin",
		" Grok ", " Gemini ",
	} {
		if ProviderSupportsQuotaObservation(provider) {
			t.Fatalf("provider %q unexpectedly supports quota observation", provider)
		}
	}
	for _, provider := range []string{"codex", "claude", "devin", "xai", "CODEX", " Claude ", " Devin ", "XAI"} {
		if !ProviderSupportsQuotaObservation(provider) {
			t.Fatalf("provider %q unexpectedly excluded from quota observation", provider)
		}
	}
}

func TestSnapshotKeepsStateWhenResponseCarriesNoSignal(t *testing.T) {
	snapshot := quotaTestSnapshot(t, "codex", "", time.Unix(100, 0),
		"X-Codex-Primary-Used-Percent", "5", "X-Codex-Primary-Window-Minutes", "10080")
	before := snapshot.Clone()
	if snapshot.ObserveResponseHeaders("codex", "", http.Header{"Content-Type": []string{"application/json"}}, time.Unix(200, 0)) {
		t.Fatal("signal-free response reported a change")
	}
	if !reflect.DeepEqual(snapshot, before) {
		t.Fatalf("snapshot was disturbed: %+v, want %+v", snapshot, before)
	}
}

func TestManagerMarkResultRecordsResponseQuotaSnapshotInMemory(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	auth, errRegister := manager.Register(context.Background(), &Auth{
		ID:       "quota-signal-auth",
		Provider: "codex",
	})
	if errRegister != nil || auth == nil {
		t.Fatalf("Register() auth=%#v err=%v", auth, errRegister)
	}

	ctx := internallogging.WithResponseHeadersHolder(context.Background())
	internallogging.SetResponseHeaders(ctx, http.Header{
		"X-Codex-Active-Limit":           []string{"codex_bengalfox"},
		"X-Codex-Primary-Used-Percent":   []string{"2"},
		"X-Codex-Primary-Window-Minutes": []string{"10080"},
		"X-Codex-Primary-Reset-At":       []string{"1782951970"},
	})
	manager.MarkResult(ctx, Result{
		AuthID:   auth.ID,
		Provider: "codex",
		Model:    "gpt-5.3-codex",
		Success:  true,
	})

	updated, ok := manager.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatal("auth not found after MarkResult")
	}
	if updated.QuotaSnapshot.Provider != ProviderCodex || updated.QuotaSnapshot.UpdatedAt.IsZero() {
		t.Fatalf("in-memory snapshot = %+v", updated.QuotaSnapshot)
	}
	primary := headersTestWindow(t, updated.QuotaSnapshot, "primary")
	headersTestAssertUsed(t, primary, 2)
	if primary.Class(ProviderCodex) != "codex:weekly" || primary.ResetAt.Unix() != 1782951970 || primary.Source != SourceHeaders {
		t.Fatalf("primary = %+v", primary)
	}
}

func TestMarkResultCountTokensDoesNotChangeSnapshot(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	auth, errRegister := manager.Register(context.Background(), &Auth{
		ID:            "quota-count-tokens-auth",
		Provider:      "claude",
		QuotaSnapshot: quotaTestSnapshot(t, "claude", "", time.Unix(10, 0), "Anthropic-Ratelimit-Unified-5h-Utilization", "0.2"),
	})
	if errRegister != nil || auth == nil {
		t.Fatalf("Register() auth=%#v err=%v", auth, errRegister)
	}
	before := auth.QuotaSnapshot.Clone()

	ctx := internallogging.WithResponseHeadersHolder(context.Background())
	internallogging.SetResponseHeaders(ctx, http.Header{
		"Anthropic-Ratelimit-Unified-5h-Utilization": []string{"0.9"},
	})
	manager.MarkResult(ctx, Result{
		AuthID:               auth.ID,
		Provider:             "claude",
		Model:                "claude-opus-4-6",
		Success:              true,
		SkipQuotaObservation: true,
	})

	updated, ok := manager.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatal("auth not found after MarkResult")
	}
	if !reflect.DeepEqual(updated.QuotaSnapshot, before) {
		t.Fatalf("count_tokens changed the last generation snapshot: %+v, want %+v", updated.QuotaSnapshot, before)
	}
	headersTestAssertUsed(t, headersTestWindow(t, updated.QuotaSnapshot, "five_hour"), 20)
}

func TestResetModelStateLeavesSnapshotUntouched(t *testing.T) {
	auth := &Auth{
		Provider:      "codex",
		QuotaSnapshot: quotaTestSnapshot(t, "codex", "", time.Unix(10, 0), "X-Codex-Primary-Used-Percent", "51", "X-Codex-Primary-Window-Minutes", "10080"),
	}
	before := auth.QuotaSnapshot.Clone()
	state := &ModelState{
		Status:        StatusError,
		Unavailable:   true,
		StatusMessage: "quota",
		Quota: QuotaState{
			Exceeded:      true,
			Reason:        "credential_quota",
			NextRecoverAt: time.Unix(20, 0),
			BackoffLevel:  2,
		},
	}
	resetModelState(state, time.Unix(30, 0))
	if state.Quota.Exceeded || state.Quota.Reason != "" || !state.Quota.NextRecoverAt.IsZero() || state.Quota.BackoffLevel != 0 {
		t.Fatalf("cooldown state was not reset: %#v", state.Quota)
	}
	if !reflect.DeepEqual(auth.QuotaSnapshot, before) {
		t.Fatalf("snapshot changed during reset: %+v", auth.QuotaSnapshot)
	}
}

func TestClearCooldownStateForAuthLeavesSnapshotUntouched(t *testing.T) {
	auth := &Auth{
		Provider:       "codex",
		Unavailable:    true,
		NextRetryAfter: time.Unix(40, 0),
		Quota:          QuotaState{Exceeded: true, Reason: "credential_quota", NextRecoverAt: time.Unix(40, 0)},
		QuotaSnapshot:  quotaTestSnapshot(t, "codex", "", time.Unix(10, 0), "X-Codex-Primary-Used-Percent", "51", "X-Codex-Primary-Window-Minutes", "10080"),
		ModelStates: map[string]*ModelState{
			"gpt-5.3-codex": {
				Unavailable:    true,
				NextRetryAfter: time.Unix(40, 0),
				Quota:          QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: time.Unix(40, 0)},
			},
		},
	}
	before := auth.QuotaSnapshot.Clone()
	if !clearCooldownStateForAuth(auth, time.Unix(50, 0)) {
		t.Fatal("clearCooldownStateForAuth reported no change")
	}
	if auth.Unavailable || auth.Quota.Exceeded || auth.Quota.Reason != "" {
		t.Fatalf("cooldown was not cleared: %#v", auth.Quota)
	}
	if state := auth.ModelStates["gpt-5.3-codex"]; state.Unavailable || state.Quota.Exceeded {
		t.Fatalf("model cooldown was not cleared: %#v", state.Quota)
	}
	if !reflect.DeepEqual(auth.QuotaSnapshot, before) {
		t.Fatalf("clearing cooldown changed the snapshot: %+v", auth.QuotaSnapshot)
	}
}

func TestMarkResultQuotaFailureDoesNotEraseSnapshot(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	auth, errRegister := manager.Register(context.Background(), &Auth{
		ID:       "quota-sibling-auth",
		Provider: "codex",
		QuotaSnapshot: quotaTestSnapshot(t, "codex", "", time.Unix(10, 0),
			"X-Codex-Primary-Used-Percent", "40", "X-Codex-Primary-Window-Minutes", "10080",
			"X-Codex-Secondary-Used-Percent", "60", "X-Codex-Secondary-Window-Minutes", "300"),
		ModelStates: map[string]*ModelState{
			"gpt-5.3-codex": {Status: StatusActive},
			"gpt-5.4":       {Status: StatusActive},
		},
	})
	if errRegister != nil || auth == nil {
		t.Fatalf("Register() auth=%#v err=%v", auth, errRegister)
	}

	ctx := internallogging.WithResponseHeadersHolder(context.Background())
	internallogging.SetResponseHeaders(ctx, http.Header{
		"Retry-After":                    []string{"120"},
		"X-Codex-Primary-Used-Percent":   []string{"99"},
		"X-Codex-Primary-Window-Minutes": []string{"10080"},
	})
	manager.MarkResult(ctx, Result{
		AuthID:          auth.ID,
		Provider:        "codex",
		Model:           "gpt-5.3-codex",
		Success:         false,
		CredentialScope: true,
		Error:           &Error{HTTPStatus: 429, Message: "quota"},
	})

	updated, ok := manager.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatal("auth not found after MarkResult")
	}
	current := updated.ModelStates["gpt-5.3-codex"]
	if current == nil || !current.Quota.Exceeded || current.Quota.Reason != "quota" {
		t.Fatalf("current model cooldown missing: %#v", current)
	}
	sibling := updated.ModelStates["gpt-5.4"]
	if sibling == nil || !sibling.Quota.Exceeded || sibling.Quota.Reason != "credential_quota" {
		t.Fatalf("sibling cooldown missing: %#v", sibling)
	}
	headersTestAssertUsed(t, headersTestWindow(t, updated.QuotaSnapshot, "primary"), 99)
	headersTestAssertUsed(t, headersTestWindow(t, updated.QuotaSnapshot, "secondary"), 60)
}

func TestMarkResultRetainedCredentialQuotaStillObservesSnapshot(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	recoverAt := time.Now().Add(time.Hour)
	auth, errRegister := manager.Register(context.Background(), &Auth{
		ID:       "quota-retain-auth",
		Provider: "codex",
		Quota: QuotaState{
			Exceeded:      true,
			Reason:        "credential_quota",
			NextRecoverAt: recoverAt,
		},
		QuotaSnapshot: quotaTestSnapshot(t, "codex", "", time.Unix(10, 0),
			"X-Codex-Primary-Used-Percent", "10", "X-Codex-Primary-Window-Minutes", "10080"),
		ModelStates: map[string]*ModelState{
			"gpt-5.3-codex": {
				Status:         StatusError,
				Unavailable:    true,
				NextRetryAfter: recoverAt,
				Quota: QuotaState{
					Exceeded:      true,
					Reason:        "credential_quota",
					NextRecoverAt: recoverAt,
				},
			},
		},
	})
	if errRegister != nil || auth == nil {
		t.Fatalf("Register() auth=%#v err=%v", auth, errRegister)
	}

	ctx := internallogging.WithResponseHeadersHolder(context.Background())
	internallogging.SetResponseHeaders(ctx, http.Header{
		"X-Codex-Primary-Used-Percent":   []string{"20"},
		"X-Codex-Primary-Window-Minutes": []string{"10080"},
	})
	manager.MarkResult(ctx, Result{
		AuthID:   auth.ID,
		Provider: "codex",
		Model:    "gpt-5.3-codex",
		Success:  true,
	})

	updated, ok := manager.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatal("auth not found after MarkResult")
	}
	if !updated.Quota.Exceeded || updated.Quota.Reason != "credential_quota" {
		t.Fatalf("retained cooldown was disturbed: %#v", updated.Quota)
	}
	state := updated.ModelStates["gpt-5.3-codex"]
	if state == nil || !state.Quota.Exceeded || state.Quota.Reason != "credential_quota" {
		t.Fatalf("model cooldown was disturbed: %#v", state)
	}
	headersTestAssertUsed(t, headersTestWindow(t, updated.QuotaSnapshot, "primary"), 20)
}

func TestRestoreCooldownRecordDoesNotOverwriteNewerSnapshotWindow(t *testing.T) {
	now := time.Now()
	nextRetry := now.Add(time.Hour).UTC().Truncate(time.Second)
	saved := quotaTestSnapshot(t, "codex", "", now.Add(-time.Hour),
		"X-Codex-Primary-Used-Percent", "1", "X-Codex-Primary-Window-Minutes", "10080")
	store := &recordingCooldownStateStore{
		load: []CooldownStateRecord{{
			Provider:       "codex",
			AuthID:         "auth-restore-obs",
			Status:         "cooling",
			NextRetryAfter: nextRetry,
			Reason:         "quota",
			Quota: QuotaState{
				Exceeded:      true,
				Reason:        "quota",
				NextRecoverAt: nextRetry,
			},
			Snapshot:  &saved,
			UpdatedAt: nextRetry.Add(-time.Minute),
		}},
	}
	manager := NewManager(nil, nil, nil)
	manager.SetCooldownStateStore(store)
	if _, errRegister := manager.Register(WithSkipPersist(context.Background()), &Auth{
		ID:       "auth-restore-obs",
		Provider: "codex",
		QuotaSnapshot: quotaTestSnapshot(t, "codex", "", now,
			"X-Codex-Primary-Used-Percent", "77", "X-Codex-Primary-Window-Minutes", "10080"),
	}); errRegister != nil {
		t.Fatalf("Register() returned error: %v", errRegister)
	}
	if errRestore := manager.RestoreCooldownStates(context.Background()); errRestore != nil {
		t.Fatalf("RestoreCooldownStates() returned error: %v", errRestore)
	}
	auth, ok := manager.GetByID("auth-restore-obs")
	if !ok || auth == nil {
		t.Fatal("restored auth was not found")
	}
	if !auth.Unavailable || !auth.Quota.Exceeded || auth.Quota.Reason != "quota" {
		t.Fatalf("cooldown was not restored: %#v", auth.Quota)
	}
	headersTestAssertUsed(t, headersTestWindow(t, auth.QuotaSnapshot, "primary"), 77)
}
