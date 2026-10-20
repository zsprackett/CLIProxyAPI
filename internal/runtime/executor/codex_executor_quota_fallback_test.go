package executor

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// codexTestPlanIDToken is an id_token whose claims carry the account id, the
// plan, and the subscription end, as a ChatGPT login stores them.
func codexTestPlanIDToken(accountID, plan, activeUntil string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID +
		`","chatgpt_plan_type":"` + plan + `","chatgpt_subscription_active_until":"` + activeUntil + `"}}`))
	return header + "." + payload + "."
}

func pollCodexQuotaWith(t *testing.T, auth *cliproxyauth.Auth, usage, subscription string, usageStatus, subscriptionStatus int) cliproxyauth.CodexExtras {
	t.Helper()
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		status, body := http.StatusOK, `{}`
		switch req.URL.Path {
		case "/backend-api/wham/usage":
			status, body = usageStatus, usage
		case "/backend-api/subscriptions":
			status, body = subscriptionStatus, subscription
		case "/backend-api/wham/rate-limit-reset-credits":
			body = `{"credits":[]}`
		}
		return &http.Response{
			StatusCode: status,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", http.RoundTripper(transport))
	obs, _ := NewCodexExecutor(&config.Config{}).PollQuota(ctx, auth, cliproxyauth.PollOnDemand)
	return obs.Extras.(cliproxyauth.CodexExtras)
}

// When the upstream leaves out the plan or the subscription end, or its request
// fails or is skipped, the card shows the values stored with the credential,
// as the management panel did before the backend owned the poll.
func TestCodexPollQuotaFallsBackToCredentialPlanAndSubscription(t *testing.T) {
	const usageWithoutPlan = `{"rate_limit":{"primary_window":{"used_percent":5,"limit_window_seconds":18000,"reset_after_seconds":60}}}`
	jwtActiveUntil := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	metaActiveUntil := time.Date(2027, 1, 15, 0, 0, 0, 0, time.UTC)

	t.Run("upstream values missing", func(t *testing.T) {
		auth := &cliproxyauth.Auth{Provider: "codex", Metadata: map[string]any{
			"access_token": "token", "id_token": codexTestPlanIDToken("acct_jwt", "plus", "2026-12-01T00:00:00Z"),
		}}
		extras := pollCodexQuotaWith(t, auth, usageWithoutPlan, `{}`, http.StatusOK, http.StatusOK)
		if extras.Plan.Status != cliproxyauth.SectionOK || extras.Plan.Value == nil || *extras.Plan.Value != "plus" {
			t.Fatalf("plan = %+v, want ok plus from the id_token", extras.Plan)
		}
		if extras.Subscription.Status != cliproxyauth.SectionOK || extras.Subscription.Value == nil || !extras.Subscription.Value.ActiveUntil.Equal(jwtActiveUntil) {
			t.Fatalf("subscription = %+v, want ok %s from the id_token", extras.Subscription, jwtActiveUntil)
		}
	})

	t.Run("requests failed", func(t *testing.T) {
		auth := &cliproxyauth.Auth{Provider: "codex", Metadata: map[string]any{
			"access_token": "token", "id_token": codexTestPlanIDToken("acct_jwt", "plus", "2026-12-01T00:00:00Z"),
		}}
		extras := pollCodexQuotaWith(t, auth, `{"error":"down"}`, `{"error":"down"}`, http.StatusInternalServerError, http.StatusInternalServerError)
		if extras.Plan.Status != cliproxyauth.SectionFailed || extras.Plan.Value == nil || *extras.Plan.Value != "plus" {
			t.Fatalf("plan = %+v, want failed with the id_token plan", extras.Plan)
		}
		if extras.Subscription.Status != cliproxyauth.SectionFailed || extras.Subscription.Value == nil || !extras.Subscription.Value.ActiveUntil.Equal(jwtActiveUntil) {
			t.Fatalf("subscription = %+v, want failed with the id_token end", extras.Subscription)
		}
	})

	t.Run("subscription skipped without an account id", func(t *testing.T) {
		auth := &cliproxyauth.Auth{Provider: "codex", Metadata: map[string]any{
			"access_token": "token", "plan_type": "Team", "chatgpt_subscription_active_until": "2027-01-15T00:00:00Z",
		}}
		extras := pollCodexQuotaWith(t, auth, usageWithoutPlan, `{}`, http.StatusOK, http.StatusOK)
		if extras.Plan.Value == nil || *extras.Plan.Value != "team" {
			t.Fatalf("plan = %+v, want team from the metadata", extras.Plan)
		}
		if extras.Subscription.Status != cliproxyauth.SectionOK || extras.Subscription.Value == nil || !extras.Subscription.Value.ActiveUntil.Equal(metaActiveUntil) {
			t.Fatalf("subscription = %+v, want ok %s from the metadata", extras.Subscription, metaActiveUntil)
		}
	})

	t.Run("upstream plan wins", func(t *testing.T) {
		auth := &cliproxyauth.Auth{Provider: "codex", Metadata: map[string]any{
			"access_token": "token", "id_token": codexTestPlanIDToken("acct_jwt", "plus", "2026-12-01T00:00:00Z"),
		}}
		extras := pollCodexQuotaWith(t, auth, `{"plan_type":"pro","rate_limit":{}}`, `{"active_until":"2026-11-01T00:00:00Z"}`, http.StatusOK, http.StatusOK)
		if extras.Plan.Value == nil || *extras.Plan.Value != "pro" {
			t.Fatalf("plan = %+v, want the upstream pro", extras.Plan)
		}
		if extras.Subscription.Value == nil || !extras.Subscription.Value.ActiveUntil.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
			t.Fatalf("subscription = %+v, want the upstream end", extras.Subscription)
		}
	})
}
