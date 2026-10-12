package management

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/quotaforecast"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func getQuotaForecast(t *testing.T, h *Handler, query string) (int, quotaforecast.Response) {
	t.Helper()
	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, "/v8/management/observability/quota/forecast"+query, nil)
	h.GetQuotaForecast(ginCtx)
	var resp quotaforecast.Response
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return rec.Code, resp
}

func TestGetQuotaForecast(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")
	cfg := &config.Config{}
	cfg.AuthDir = t.TempDir()
	manager := coreauth.NewManager(nil, nil, nil)
	manager.SetConfig(cfg)
	h := NewHandlerWithoutConfigFilePath(cfg, manager)

	if code, resp := getQuotaForecast(t, h, ""); code != http.StatusOK || resp.Enabled || resp.Reason != "disabled" {
		t.Fatalf("disabled: code=%d resp=%+v", code, resp)
	}
	if code, _ := getQuotaForecast(t, h, "?lookback_hours=abc"); code != http.StatusBadRequest {
		t.Fatalf("bad lookback: code=%d", code)
	}

	cfg.QuotaForecast.Enabled = true
	manager.SetConfig(cfg)
	code, resp := getQuotaForecast(t, h, "?provider=claude&lookback_hours=6")
	if code != http.StatusOK || !resp.Enabled || resp.LookbackHours != 6 || resp.Pools == nil || resp.Alerts == nil {
		t.Fatalf("enabled: code=%d resp=%+v", code, resp)
	}
}
