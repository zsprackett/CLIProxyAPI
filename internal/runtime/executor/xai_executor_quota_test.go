package executor

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// The monthly payload Grok returned on 2026-10-19 for a weekly-plan account
// with no additional usage limit set.
const xaiBillingMonthlyNoLimitBody = `{"config":{"monthlyLimit":{"val":0},"used":{"val":67},"onDemandCap":{"val":0},` +
	`"billingPeriodStart":"2026-10-01T00:00:00+00:00","billingPeriodEnd":"2026-11-01T00:00:00+00:00"}}`

func pollXAIQuotaWith(t *testing.T, weeklyStatus int, weeklyBody string, monthlyBody string) (http.Header, error) {
	t.Helper()
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, monthlyBody
		if req.URL.Query().Get("format") == "credits" {
			status, body = weeklyStatus, weeklyBody
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", http.RoundTripper(transport))
	auth := &cliproxyauth.Auth{
		Provider: "xai",
		Metadata: map[string]any{"access_token": "token", "auth_kind": "oauth"},
	}
	return NewXAIExecutor(&config.Config{}).PollQuota(ctx, auth)
}

func TestXAIPollQuotaFailsWhenWeeklyBillingFails(t *testing.T) {
	headers, errPoll := pollXAIQuotaWith(t, http.StatusServiceUnavailable,
		`{"code":"The service is currently unavailable","error":"cursor billing: snapshot failed"}`,
		xaiBillingMonthlyNoLimitBody)
	if errPoll == nil {
		t.Fatalf("PollQuota() error = nil, headers = %v; want the weekly billing error so the last snapshot stays", headers)
	}
	if headers != nil {
		t.Fatalf("PollQuota() headers = %v, want nil", headers)
	}
}

func TestXAIPollQuotaUsesMonthlyWhenWeeklyReportsNoPeriod(t *testing.T) {
	headers, errPoll := pollXAIQuotaWith(t, http.StatusOK, `{"config":{}}`,
		`{"config":{"monthlyLimit":{"val":1000},"used":{"val":250},`+
			`"billingPeriodStart":"2026-10-01T00:00:00+00:00","billingPeriodEnd":"2026-11-01T00:00:00+00:00"}}`)
	if errPoll != nil {
		t.Fatalf("PollQuota() error = %v", errPoll)
	}
	if got := headers.Get("X-Xai-Billing-Period-Type"); got != "monthly" {
		t.Fatalf("period type = %q, want monthly", got)
	}
	if got := headers.Get("X-Xai-Billing-Used-Percent"); got != "25" {
		t.Fatalf("used percent = %q, want 25", got)
	}
}
