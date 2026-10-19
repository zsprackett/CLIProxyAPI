package management

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
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

func listAuthFilesEntries(t *testing.T, h *Handler, path string, v8 bool) []map[string]any {
	t.Helper()
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(http.MethodGet, path, nil)
	if v8 {
		ctx.Set(ConfigV8ContextKey, true)
	}
	h.ListAuthFiles(ctx)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Files []map[string]any `json:"files"`
	}
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &payload); errDecode != nil {
		t.Fatal(errDecode)
	}
	return payload.Files
}

func TestListAuthFilesServesQuotaSnapshotOnV8Only(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	manager := coreauth.NewManager(nil, nil, nil)
	cfg := &config.Config{AuthDir: t.TempDir()}
	manager.SetConfig(cfg)
	auth := observedCodexAuth(t, "codex")
	auth.ID, auth.Index = "codex-a", "index-a"
	auth.Attributes = map[string]string{"runtime_only": "true"}
	registerAuthForLookupTest(t, manager, auth)
	h := NewHandlerWithoutConfigFilePath(cfg, manager)

	v8 := listAuthFilesEntries(t, h, "/v8/management/credentials", true)
	if len(v8) != 1 {
		t.Fatalf("v8 files = %+v", v8)
	}
	snapshot, _ := v8[0]["quota_snapshot"].(map[string]any)
	if snapshot["provider"] != "codex" {
		t.Fatalf("v8 listing must carry quota_snapshot: %+v", v8[0])
	}
	for _, removed := range []string{"quota", "model_quotas"} {
		if _, ok := v8[0][removed]; ok {
			t.Fatalf("v8 listing still carries %q: %+v", removed, v8[0])
		}
	}

	v0 := listAuthFilesEntries(t, h, "/v0/management/auth-files", false)
	if len(v0) != 1 {
		t.Fatalf("v0 files = %+v", v0)
	}
	if _, ok := v0[0]["quota_snapshot"]; ok {
		t.Fatalf("v0 listing must not gain quota_snapshot: %+v", v0[0])
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
