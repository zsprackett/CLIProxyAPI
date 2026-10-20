package executor

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func codexTestIDToken(accountID string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_account_id":"` + accountID + `"}}`))
	return header + "." + payload + "."
}

func TestCodexQuotaAccountID(t *testing.T) {
	cases := map[string]struct {
		auth *cliproxyauth.Auth
		want string
	}{
		"stored account_id":                   {&cliproxyauth.Auth{Metadata: map[string]any{"account_id": " acct_meta "}}, "acct_meta"},
		"stored chatgpt_account_id attribute": {&cliproxyauth.Auth{Attributes: map[string]string{"chatgpt_account_id": "acct_attr"}}, "acct_attr"},
		"id_token claims":                     {&cliproxyauth.Auth{Metadata: map[string]any{"id_token": codexTestIDToken("acct_jwt")}}, "acct_jwt"},
		"stored id wins over id_token":        {&cliproxyauth.Auth{Metadata: map[string]any{"account_id": "acct_meta", "id_token": codexTestIDToken("acct_jwt")}}, "acct_meta"},
		"nothing":                             {&cliproxyauth.Auth{Metadata: map[string]any{"id_token": "not-a-jwt"}}, ""},
	}
	for name, tc := range cases {
		if got := codexQuotaAccountID(tc.auth); got != tc.want {
			t.Errorf("%s: account id = %q, want %q", name, got, tc.want)
		}
	}
}

func TestCodexPollQuotaFetchesThreeSections(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]http.Header{}
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		seen[req.URL.Path+"?"+req.URL.RawQuery] = req.Header.Clone()
		mu.Unlock()
		status, body := http.StatusOK, `{}`
		switch req.URL.Path {
		case "/backend-api/wham/usage":
			body = `{"plan_type":"plus","rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":5,"limit_window_seconds":18000,"reset_at":1792432800}}}`
		case "/backend-api/subscriptions":
			body = `{"active_until":"2026-11-01T00:00:00Z"}`
		case "/backend-api/wham/rate-limit-reset-credits":
			status, body = http.StatusInternalServerError, `{"error":"down"}`
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
		Provider: "codex",
		Metadata: map[string]any{"access_token": "token", "id_token": codexTestIDToken("acct_jwt")},
	}
	obs, errPoll := NewCodexExecutor(&config.Config{}).PollQuota(ctx, auth, cliproxyauth.PollOnDemand)
	if errPoll != nil {
		t.Fatalf("PollQuota() error = %v", errPoll)
	}
	for _, key := range []string{"/backend-api/wham/usage?", "/backend-api/subscriptions?account_id=acct_jwt", "/backend-api/wham/rate-limit-reset-credits?"} {
		headers, ok := seen[key]
		if !ok {
			t.Fatalf("request %s not made; saw %v", key, seen)
		}
		if headers.Get("Chatgpt-Account-Id") != "acct_jwt" {
			t.Fatalf("%s sent account id %q", key, headers.Get("Chatgpt-Account-Id"))
		}
	}
	if got := seen["/backend-api/wham/rate-limit-reset-credits?"].Get("OpenAI-Beta"); got != "codex-1" {
		t.Fatalf("reset credits beta header = %q", got)
	}
	if len(obs.Windows) != 1 || obs.Windows[0].ID != "primary" {
		t.Fatalf("windows = %+v", obs.Windows)
	}
	extras := obs.Extras.(cliproxyauth.CodexExtras)
	if *extras.Plan.Value != "plus" || extras.Subscription.Status != cliproxyauth.SectionOK || extras.ResetCredits.Status != cliproxyauth.SectionFailed {
		t.Fatalf("extras = plan %+v subscription %s reset credits %s", extras.Plan, extras.Subscription.Status, extras.ResetCredits.Status)
	}
}

func TestCodexPollQuotaSkipsAPIKeys(t *testing.T) {
	auth := &cliproxyauth.Auth{Provider: "codex", Attributes: map[string]string{"api_key": "sk-test"}}
	if _, errPoll := NewCodexExecutor(&config.Config{}).PollQuota(context.Background(), auth, cliproxyauth.PollBackground); errPoll != cliproxyauth.ErrQuotaNotPollable {
		t.Fatalf("error = %v, want ErrQuotaNotPollable", errPoll)
	}
}
