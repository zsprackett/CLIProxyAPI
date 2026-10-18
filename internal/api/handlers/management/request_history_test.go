package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/requestlog"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

func getRequestHistory(t *testing.T, h *Handler, query string) (int, requestlog.Page) {
	t.Helper()
	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/v8/management/observability/requests"+query, nil)
	h.GetRequestHistory(ginCtx)
	var page requestlog.Page
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return rec.Code, page
}

func getRequestSessions(t *testing.T, h *Handler, query string) (int, requestlog.SessionPage) {
	t.Helper()
	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/v8/management/observability/requests/sessions"+query, nil)
	h.GetRequestSessions(ginCtx)
	var page requestlog.SessionPage
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return rec.Code, page
}

func TestGetRequestHistory(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	cfg := &config.Config{}
	cfg.AuthDir = t.TempDir()
	manager := coreauth.NewManager(nil, nil, nil)
	manager.SetConfig(cfg)
	h := NewHandlerWithoutConfigFilePath(cfg, manager)
	svc := requestlog.Default()
	svc.Start(context.Background(), manager)
	defer svc.Stop(context.Background())

	if code, page := getRequestHistory(t, h, ""); code != http.StatusOK || page.Enabled || page.Reason != "disabled" || page.Entries == nil {
		t.Fatalf("disabled: code=%d page=%+v", code, page)
	}
	for _, bad := range []string{"?limit=abc", "?limit=0", "?since=x", "?before=-1", "?from=notatime", "?to=1x", "?verdict=bogus", "?verdict=hit,bogus", "?status=weird", "?status=42", "?order=sideways"} {
		if code, _ := getRequestHistory(t, h, bad); code != http.StatusBadRequest {
			t.Errorf("%s: code=%d, want 400", bad, code)
		}
	}

	cfg.RequestHistory.Enabled = true
	manager.SetConfig(cfg)
	// A hot enable takes effect on the next query (or tick); records before that are not kept.
	if code, page := getRequestHistory(t, h, ""); code != http.StatusOK || !page.Enabled || len(page.Entries) != 0 {
		t.Fatalf("just enabled: code=%d page=%+v", code, page)
	}
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	svc.HandleUsage(context.Background(), coreusage.Record{Provider: "claude", Model: "opus", SessionID: "s1", AuthID: "a", RequestedAt: at, Detail: coreusage.Detail{InputTokens: 5}})
	svc.HandleUsage(context.Background(), coreusage.Record{Provider: "claude", Model: "opus", SessionID: "s1", AuthID: "a", RequestedAt: at.Add(time.Minute), Failed: true, Fail: coreusage.Failure{StatusCode: 500}})
	code, page := getRequestHistory(t, h, "?session=s1&order=asc&status=ok&verdict=na,cold&from="+at.Format(time.RFC3339)+"&to="+at.Add(2*time.Minute).Format(time.RFC3339)+"&limit=10")
	if code != http.StatusOK || !page.Enabled || len(page.Entries) != 1 || page.Entries[0].SessionID != "s1" || page.Entries[0].Cache.Verdict != "na" {
		t.Fatalf("enabled: code=%d page=%+v", code, page)
	}
	if code, page = getRequestHistory(t, h, "?status=500&from="+strconv.FormatInt(at.UnixMilli(), 10)); code != http.StatusOK || len(page.Entries) != 1 || !page.Entries[0].Failed {
		t.Fatalf("status code and unix ms from: code=%d page=%+v", code, page)
	}
	if code, page = getRequestHistory(t, h, "?since="+strconv.FormatUint(page.Cursor, 10)); code != http.StatusOK || len(page.Entries) != 0 {
		t.Fatalf("since cursor: code=%d entries=%d", code, len(page.Entries))
	}

	if code, _ := getRequestSessions(t, h, "?limit=x"); code != http.StatusBadRequest {
		t.Fatalf("sessions bad limit: code=%d", code)
	}
	code, sessions := getRequestSessions(t, h, "?model=opus&auth=a")
	if code != http.StatusOK || !sessions.Enabled || len(sessions.Sessions) != 1 || sessions.Sessions[0].SessionID != "s1" || sessions.Sessions[0].Requests != 2 {
		t.Fatalf("sessions: code=%d page=%+v", code, sessions)
	}
}
