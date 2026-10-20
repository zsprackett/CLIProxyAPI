package management

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// quotaPollStubExecutor answers PollQuota with a fixed observation or error.
type quotaPollStubExecutor struct {
	provider string
	obs      coreauth.QuotaObservation
	err      error
	calls    int
}

func (e *quotaPollStubExecutor) Identifier() string { return e.provider }
func (e *quotaPollStubExecutor) Execute(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}
func (e *quotaPollStubExecutor) ExecuteStream(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	return nil, nil
}
func (e *quotaPollStubExecutor) CountTokens(context.Context, *coreauth.Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}
func (e *quotaPollStubExecutor) HttpRequest(context.Context, *coreauth.Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}
func (e *quotaPollStubExecutor) Refresh(_ context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	return auth, nil
}
func (e *quotaPollStubExecutor) PollQuota(context.Context, *coreauth.Auth, coreauth.PollTrigger) (coreauth.QuotaObservation, error) {
	e.calls++
	return e.obs, e.err
}

func claudePollObservation(now time.Time) coreauth.QuotaObservation {
	return coreauth.QuotaObservation{
		Provider:   coreauth.ProviderClaude,
		Source:     coreauth.SourcePoll,
		ObservedAt: now,
		Sections:   map[coreauth.SectionID]coreauth.SectionStatus{coreauth.SectionClaudeUsage: coreauth.SectionOK},
		Windows: []coreauth.QuotaWindowObs{{
			ID: "seven_day", Used: coreauth.KnownPercent(42), Period: 7 * 24 * time.Hour, ResetAt: now.Add(48 * time.Hour),
			Section: coreauth.SectionClaudeUsage, Source: coreauth.SourcePoll, ObservedAt: now,
		}},
		Extras: coreauth.ClaudeExtras{Plan: coreauth.OKSection("plan_max", now)},
	}
}

func newQuotaPollHandler(t *testing.T, exec *quotaPollStubExecutor, cfg *config.Config) (*Handler, string) {
	t.Helper()
	manager := coreauth.NewManager(nil, nil, nil)
	manager.RegisterExecutor(exec)
	if cfg != nil {
		manager.SetConfig(cfg)
	}
	auth := &coreauth.Auth{ID: "claude-poll.json", FileName: "claude-poll.json", Provider: exec.provider, Status: coreauth.StatusActive}
	authIndex := auth.EnsureIndex()
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register: %v", errRegister)
	}
	return NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: t.TempDir()}, manager), authIndex
}

func postQuotaPoll(h *Handler, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/v8/management/credentials/quota/poll", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx.Request = req
	h.PollCredentialQuota(ctx)
	return rec
}

func TestPollCredentialQuotaReturnsSnapshot(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 10, 19, 17, 0, 0, 0, time.UTC)
	exec := &quotaPollStubExecutor{provider: "claude", obs: claudePollObservation(now)}
	h, authIndex := newQuotaPollHandler(t, exec, nil)

	rec := postQuotaPoll(h, `{"auth_index":"`+authIndex+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Snapshot coreauth.QuotaSnapshot `json:"quota_snapshot"`
	}
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &payload); errDecode != nil {
		t.Fatalf("decode: %v", errDecode)
	}
	snapshot := payload.Snapshot
	if snapshot.Provider != coreauth.ProviderClaude || len(snapshot.Windows) != 1 || snapshot.Windows[0].ID != "seven_day" {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if snapshot.LastPoll == nil || snapshot.LastPoll.Trigger != coreauth.PollOnDemand || snapshot.LastPoll.Error != "" {
		t.Fatalf("last poll = %+v, want an on_demand poll with no error", snapshot.LastPoll)
	}
	if extras, ok := snapshot.Extras.(coreauth.ClaudeExtras); !ok || extras.Plan.Value == nil || *extras.Plan.Value != "plan_max" {
		t.Fatalf("extras = %+v", snapshot.Extras)
	}
	if exec.calls != 1 {
		t.Fatalf("upstream calls = %d, want 1", exec.calls)
	}
	if !strings.Contains(rec.Body.String(), `"class":"claude:7d"`) {
		t.Fatalf("response windows carry no class: %s", rec.Body.String())
	}
}

func TestPollCredentialQuotaUpstreamFailureIsOKWithError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 10, 19, 17, 0, 0, 0, time.UTC)
	exec := &quotaPollStubExecutor{provider: "claude", obs: claudePollObservation(now)}
	h, authIndex := newQuotaPollHandler(t, exec, nil)
	if rec := postQuotaPoll(h, `{"auth_index":"`+authIndex+`"}`); rec.Code != http.StatusOK {
		t.Fatalf("first poll status = %d body = %s", rec.Code, rec.Body.String())
	}

	exec.obs, exec.err = coreauth.QuotaObservation{}, errors.New("fetch Claude OAuth usage failed with status 503")
	rec := postQuotaPoll(h, `{"auth_index":"`+authIndex+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Snapshot coreauth.QuotaSnapshot `json:"quota_snapshot"`
	}
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &payload); errDecode != nil {
		t.Fatalf("decode: %v", errDecode)
	}
	if payload.Snapshot.LastPoll == nil || payload.Snapshot.LastPoll.Error != "fetch Claude OAuth usage failed with status 503" {
		t.Fatalf("last poll = %+v, want the upstream error", payload.Snapshot.LastPoll)
	}
	if len(payload.Snapshot.Windows) != 1 {
		t.Fatalf("windows = %+v, want the stale window kept", payload.Snapshot.Windows)
	}
}

func TestPollCredentialQuotaErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	exec := &quotaPollStubExecutor{provider: "claude", err: coreauth.ErrQuotaNotPollable}
	h, authIndex := newQuotaPollHandler(t, exec, nil)

	if rec := postQuotaPoll(h, `{"auth_index":"does-not-exist"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown auth status = %d body = %s", rec.Code, rec.Body.String())
	}
	if rec := postQuotaPoll(h, `{}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing auth_index status = %d body = %s", rec.Code, rec.Body.String())
	}
	if rec := postQuotaPoll(h, `{"auth_index":"`+authIndex+`"}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("not pollable status = %d body = %s", rec.Code, rec.Body.String())
	}
	listed, _ := h.authManager.GetByID("claude-poll.json")
	if !listed.QuotaSnapshot.IsZero() {
		t.Fatalf("a refused poll wrote a snapshot: %+v", listed.QuotaSnapshot)
	}

	cfg := &config.Config{}
	cfg.Home.Enabled = true
	homeExec := &quotaPollStubExecutor{provider: "claude", obs: claudePollObservation(time.Now())}
	homeHandler, homeIndex := newQuotaPollHandler(t, homeExec, cfg)
	if rec := postQuotaPoll(homeHandler, `{"auth_index":"`+homeIndex+`"}`); rec.Code != http.StatusConflict {
		t.Fatalf("home mode status = %d body = %s", rec.Code, rec.Body.String())
	}
	if homeExec.calls != 0 {
		t.Fatalf("home mode polled upstream %d times", homeExec.calls)
	}
}
