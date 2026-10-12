package quotaforecast

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func shortPool(now time.Time, exhaustsIn, recoversIn time.Duration) Pool {
	exhausts, recovers := now.Add(exhaustsIn), now.Add(recoversIn)
	return Pool{
		Class: "claude:7d", Provider: "claude", WindowSeconds: 7 * 86400, RemainingAccounts: 1.2,
		Burn:   Burn{Sufficient: true},
		Runway: Runway{Status: "short", ExhaustsAt: &exhausts, RecoversAt: &recovers, ShortAccounts: 0.4},
	}
}

func TestAlertFiresOncePerResetCycle(t *testing.T) {
	h := newHistory()
	now := testNow
	evaluate := func(pools []Pool) int {
		fired := len(h.evaluateAlerts(pools, now, 0))
		now = now.Add(time.Minute)
		return fired
	}
	short := func() []Pool { return []Pool{shortPool(now, 10*time.Hour, 20*time.Hour)} }
	if evaluate(short()) != 0 || evaluate(short()) != 0 {
		t.Fatal("alert must wait for consecutive evaluations")
	}
	if evaluate(short()) != 1 {
		t.Fatal("alert should fire on the third consecutive shortfall")
	}
	recoversAt := h.alerts["claude:7d"].RecoversAt
	for i := 0; i < alertResolveAfter; i++ {
		evaluate(nil)
	}
	if h.alerts["claude:7d"].State != alertResolved {
		t.Fatalf("state = %s, want resolved", h.alerts["claude:7d"].State)
	}
	for i := 0; i < 5; i++ {
		if evaluate(short()) != 0 {
			t.Fatal("a flapping pool must not notify twice in one cycle")
		}
	}
	if h.alerts["claude:7d"].State != alertFiring {
		t.Fatal("renewed shortfall should show the alert again")
	}
	now = time.Unix(h.alerts["claude:7d"].RearmAt, 0)
	if h.alerts["claude:7d"].RearmAt != recoversAt {
		t.Fatal("alert should re-arm at the recovery time it waited for")
	}
	fired := 0
	for i := 0; i < 3; i++ {
		fired += evaluate(short())
	}
	if fired != 1 {
		t.Fatalf("after re-arm fired %d times, want 1", fired)
	}
}

func TestAlertLeadHoursSuppressesDistantShortfalls(t *testing.T) {
	h := newHistory()
	for i := 0; i < 5; i++ {
		if fired := h.evaluateAlerts([]Pool{shortPool(testNow, 30*time.Hour, 40*time.Hour)}, testNow, 24*time.Hour); len(fired) != 0 {
			t.Fatal("shortfall beyond the lead time must not alert")
		}
	}
	if len(h.alerts) != 0 {
		t.Fatalf("no alert should be pending: %+v", h.alerts)
	}
}

func TestPostWebhookSendsPayload(t *testing.T) {
	var got webhookPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &got); err != nil {
			t.Errorf("decode: %v", err)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content type = %q", r.Header.Get("Content-Type"))
		}
	}))
	defer server.Close()
	rec := alertRecord{Class: "claude:7d", Provider: "claude", ExhaustsAt: testNow.Add(28 * time.Hour).Unix(), RecoversAt: testNow.Add(34 * time.Hour).Unix(), ShortAccounts: 0.6, RemainingAccounts: 2.4}
	if err := postWebhook(&config.SDKConfig{}, server.URL, newWebhookPayload(rec, testNow)); err != nil {
		t.Fatal(err)
	}
	if got.Event != "quota_exhaustion_projected" || got.ShortSeconds != 6*3600 || !strings.Contains(got.Text, "1d 4h") || !strings.Contains(got.Text, "6h 0m before the next reset") {
		t.Fatalf("payload = %+v", got)
	}

	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	defer failing.Close()
	err := postWebhook(&config.SDKConfig{}, failing.URL+"/secret-token", newWebhookPayload(rec, testNow))
	if err == nil || strings.Contains(err.Error(), "secret-token") {
		t.Fatalf("error = %v, want a status error without the URL", err)
	}
}
