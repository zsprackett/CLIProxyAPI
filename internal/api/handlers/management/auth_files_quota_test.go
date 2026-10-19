package management

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func observedCodexAuth(t *testing.T, provider string) *coreauth.Auth {
	t.Helper()
	auth := &coreauth.Auth{Provider: provider}
	auth.QuotaSnapshot.ObserveResponseHeaders("codex", "", http.Header{
		"X-Codex-Primary-Used-Percent":   []string{"51"},
		"X-Codex-Primary-Window-Minutes": []string{"10080"},
		"X-Codex-Plan-Type":              []string{"pro"},
	}, time.Unix(10, 0))
	if auth.QuotaSnapshot.IsZero() {
		t.Fatal("test fixture produced an empty snapshot")
	}
	return auth
}

func TestQuotaSnapshotPayloadOmitsUnsupportedProviders(t *testing.T) {
	for _, provider := range []string{"grok", "gemini", "gemini-interactions", "openai", "openai-compatibility", "plugin-provider"} {
		got := quotaSnapshotPayload(observedCodexAuth(t, provider))
		if !got.IsZero() {
			t.Fatalf("provider %q returned a snapshot: %#v", provider, got)
		}
	}
	if got := quotaSnapshotPayload(nil); !got.IsZero() {
		t.Fatalf("nil auth returned a snapshot: %#v", got)
	}
}

func TestQuotaSnapshotPayloadServesSupportedProviderWindows(t *testing.T) {
	got := quotaSnapshotPayload(observedCodexAuth(t, "codex"))
	if got.Provider != coreauth.ProviderCodex || len(got.Windows) != 1 || got.Windows[0].ID != "primary" {
		t.Fatalf("snapshot = %#v, want the codex primary window", got)
	}
	if used, known := got.Windows[0].Used.Get(); !known || used != 51 {
		t.Fatalf("primary used = %v (known %t), want 51", used, known)
	}
}

func TestQuotaSnapshotPayloadExcludesCooldownState(t *testing.T) {
	auth := observedCodexAuth(t, "codex")
	auth.Quota = coreauth.QuotaState{
		Exceeded:      true,
		Reason:        "credential_quota",
		NextRecoverAt: time.Unix(20, 0),
		BackoffLevel:  3,
	}
	raw, errMarshal := json.Marshal(quotaSnapshotPayload(auth))
	if errMarshal != nil {
		t.Fatal(errMarshal)
	}
	var payload map[string]any
	if errUnmarshal := json.Unmarshal(raw, &payload); errUnmarshal != nil {
		t.Fatal(errUnmarshal)
	}
	for _, leaked := range []string{"exceeded", "reason", "next_recover_at", "backoff_level"} {
		if _, ok := payload[leaked]; ok {
			t.Fatalf("cooldown field %q leaked: %#v", leaked, payload)
		}
	}
}

func TestQuotaSnapshotPayloadIsACopy(t *testing.T) {
	auth := observedCodexAuth(t, "codex")
	got := quotaSnapshotPayload(auth)
	got.Windows[0].LimitReached = true
	if auth.QuotaSnapshot.Windows[0].LimitReached {
		t.Fatal("mutating the payload changed the auth snapshot")
	}
}
