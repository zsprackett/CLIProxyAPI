package helps

import (
	"net/http"
	"reflect"
	"testing"
	"time"
)

var xaiTestNow = time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)

func TestXAIBillingQuotaHeadersPrefersWeekly(t *testing.T) {
	weekly := []byte(`{"config":{
		"currentPeriod": {"type": "BILLING_PERIOD_TYPE_WEEKLY", "start": "2026-10-05T00:00:00Z", "end": "2026-10-12T00:00:00Z"},
		"creditUsagePercent": 37.5
	}}`)
	monthly := []byte(`{"config":{"monthlyLimit": {"val": "1000"}, "used": {"val": "250"}}}`)
	want := http.Header{
		"X-Xai-Billing-Period-Type":    []string{"weekly"},
		"X-Xai-Billing-Used-Percent":   []string{"37.5"},
		"X-Xai-Billing-Reset-At":       []string{"1791763200"},
		"X-Xai-Billing-Window-Minutes": []string{"10080"},
	}
	if got := XAIBillingQuotaHeaders(weekly, monthly, xaiTestNow); !reflect.DeepEqual(got, want) {
		t.Fatalf("headers = %#v, want %#v", got, want)
	}
}

func TestXAIBillingQuotaHeadersFallsBackToMonthly(t *testing.T) {
	monthly := []byte(`{"config":{
		"monthlyLimit": {"val": "1000"},
		"used": 1200,
		"billingPeriodStart": "2026-10-01T00:00:00Z",
		"billingPeriodEnd": "2026-11-01T00:00:00Z"
	}}`)
	want := http.Header{
		"X-Xai-Billing-Period-Type":    []string{"monthly"},
		"X-Xai-Billing-Used-Percent":   []string{"100"},
		"X-Xai-Billing-Reset-At":       []string{"1793491200"},
		"X-Xai-Billing-Window-Minutes": []string{"44640"},
	}
	if got := XAIBillingQuotaHeaders(nil, monthly, xaiTestNow); !reflect.DeepEqual(got, want) {
		t.Fatalf("headers = %#v, want %#v", got, want)
	}
	if got := XAIBillingQuotaHeaders([]byte(`{"config":{}}`), nil, xaiTestNow); got != nil {
		t.Fatalf("empty billing produced headers: %#v", got)
	}
}

func TestXAIBillingQuotaHeadersReadsOmittedUsageAsZeroInActivePeriod(t *testing.T) {
	// A unified-billing weekly answer at 0% omits creditUsagePercent entirely.
	weekly := []byte(`{"config":{
		"currentPeriod": {"type": "USAGE_PERIOD_TYPE_WEEKLY", "start": "2026-10-08T17:45:17.632369+00:00", "end": "2026-10-15T17:45:17.632369+00:00"},
		"onDemandCap": {"val": 0},
		"onDemandUsed": {"val": 0},
		"isUnifiedBillingUser": true,
		"prepaidBalance": {"val": 0}
	}}`)
	inPeriod := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	if got := XAIBillingQuotaHeaders(weekly, nil, inPeriod).Get("X-Xai-Billing-Used-Percent"); got != "0" {
		t.Fatalf("used percent in active period = %q, want 0", got)
	}
	// Outside the reported period the omission says nothing about current usage.
	afterPeriod := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	headers := XAIBillingQuotaHeaders(weekly, nil, afterPeriod)
	if got := headers.Get("X-Xai-Billing-Used-Percent"); got != "" {
		t.Fatalf("used percent after period = %q, want none", got)
	}
	if got := headers.Get("X-Xai-Billing-Period-Type"); got != "weekly" {
		t.Fatalf("period type = %q, want weekly", got)
	}
	// A present but malformed value is not an omission and stays unknown.
	malformed := []byte(`{"config":{
		"currentPeriod": {"type": "USAGE_PERIOD_TYPE_WEEKLY", "start": "2026-10-08T17:45:17Z", "end": "2026-10-15T17:45:17Z"},
		"creditUsagePercent": "not-a-number"
	}}`)
	if got := XAIBillingQuotaHeaders(malformed, nil, inPeriod).Get("X-Xai-Billing-Used-Percent"); got != "" {
		t.Fatalf("malformed used percent = %q, want none", got)
	}
}
