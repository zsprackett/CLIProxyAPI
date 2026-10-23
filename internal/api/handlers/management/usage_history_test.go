package management

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory"
)

type fakeUsageHistory struct {
	got usagehistory.Query
	err error
}

func (f *fakeUsageHistory) Report(_ context.Context, q usagehistory.Query) (usagehistory.Report, error) {
	f.got = q
	return usagehistory.Report{Enabled: true, Buckets: []usagehistory.Bucket{}}, f.err
}

func (f *fakeUsageHistory) Prices(context.Context) usagehistory.PriceReport {
	return usagehistory.PriceReport{Enabled: true, Models: []usagehistory.ModelPrice{{Provider: "claude", Model: "m"}}}
}

func serveUsageHistory(t *testing.T, handler func(*Handler, *gin.Context), path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(rec)
	ginCtx.Request = httptest.NewRequest(http.MethodGet, path, nil)
	handler(NewHandlerWithoutConfigFilePath(&config.Config{}, nil), ginCtx)
	return rec
}

func withUsageHistory(t *testing.T, src usageHistorySource) {
	t.Helper()
	saved := usageHistoryService
	usageHistoryService = func() usageHistorySource { return src }
	t.Cleanup(func() { usageHistoryService = saved })
}

func TestGetUsageHistoryRejectsBadQueries(t *testing.T) {
	withUsageHistory(t, &fakeUsageHistory{})
	for _, q := range []string{"?bucket=week", "?tz=Mars/Base", "?from=2026-02-30", "?from=2026-03-02&to=2026-03-01", "?model=a&model=b"} {
		rec := serveUsageHistory(t, (*Handler).GetUsageHistory, "/v8/management/observability/usage/history"+q)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "error") {
			t.Errorf("%s: status %d body %s", q, rec.Code, rec.Body.String())
		}
	}
}

func TestGetUsageHistoryPassesTheQuery(t *testing.T) {
	fake := &fakeUsageHistory{}
	withUsageHistory(t, fake)
	rec := serveUsageHistory(t, (*Handler).GetUsageHistory, "/v8/management/observability/usage/history?bucket=hour&group=agent&tz=America/Toronto")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	if fake.got.Unit != usagehistory.UnitHour || fake.got.Group != usagehistory.DimAgent || fake.got.Loc.String() != "America/Toronto" {
		t.Fatalf("query = %+v", fake.got)
	}
}

func TestGetUsageHistoryTooLarge(t *testing.T) {
	withUsageHistory(t, &fakeUsageHistory{err: usagehistory.ErrTooLarge{Cells: 60000, Limit: 50000}})
	rec := serveUsageHistory(t, (*Handler).GetUsageHistory, "/v8/management/observability/usage/history")
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "50000") {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestGetUsageHistoryDisabled(t *testing.T) {
	// The process-wide service is never started in this test binary.
	rec := serveUsageHistory(t, (*Handler).GetUsageHistory, "/v8/management/observability/usage/history")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["enabled"] != false || body["reason"] != "disabled" {
		t.Fatalf("body = %s", rec.Body.String())
	}
	prices := serveUsageHistory(t, (*Handler).GetUsagePrices, "/v8/management/observability/usage/prices")
	if prices.Code != http.StatusOK || !strings.Contains(prices.Body.String(), `"enabled":false`) {
		t.Fatalf("prices status %d body %s", prices.Code, prices.Body.String())
	}
}

func TestGetUsagePrices(t *testing.T) {
	withUsageHistory(t, &fakeUsageHistory{})
	rec := serveUsageHistory(t, (*Handler).GetUsagePrices, "/v8/management/observability/usage/prices")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"model":"m"`) {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}
