package executor

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// pollCodexQuotaHeaders runs PollQuota and returns the headers of each
// request by URL path.
func pollCodexQuotaHeaders(t *testing.T, cfg *config.Config, auth *cliproxyauth.Auth) map[string]http.Header {
	t.Helper()
	var mu sync.Mutex
	seen := map[string]http.Header{}
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		seen[req.URL.Path] = req.Header.Clone()
		mu.Unlock()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    req,
		}, nil
	})
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", http.RoundTripper(transport))
	if _, errPoll := NewCodexExecutor(cfg).PollQuota(ctx, auth, cliproxyauth.PollOnDemand); errPoll != nil {
		t.Fatalf("PollQuota() error = %v", errPoll)
	}
	return seen
}

// codexResponsesIdentity returns the User-Agent and Originator that the
// /responses path sends for a request without client headers.
func codexResponsesIdentity(t *testing.T, cfg *config.Config, auth *cliproxyauth.Auth) (string, string) {
	t.Helper()
	req, errReq := http.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", nil)
	if errReq != nil {
		t.Fatal(errReq)
	}
	applyCodexHeaders(req, auth, "token", true, cfg, http.Header{})
	return req.Header.Get("User-Agent"), req.Header.Get("Originator")
}

func TestCodexPollQuotaCloakingOverridesAuthIdentityHeaders(t *testing.T) {
	cfg := &config.Config{}
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Attributes: map[string]string{
			"header:User-Agent": "not-codex",
			"header:Originator": "not-codex",
		},
		Metadata: map[string]any{"access_token": "token", "account_id": "acct"},
	}
	wantUA, wantOriginator := codexResponsesIdentity(t, cfg, auth)
	if wantUA != codexUserAgent || wantOriginator != codexOriginator {
		t.Fatalf("/responses identity = %q / %q, want the cloaked %q / %q", wantUA, wantOriginator, codexUserAgent, codexOriginator)
	}
	seen := pollCodexQuotaHeaders(t, cfg, auth)
	for _, path := range []string{"/backend-api/wham/usage", "/backend-api/subscriptions"} {
		headers := seen[path]
		if headers == nil {
			t.Fatalf("request %s not made; saw %v", path, seen)
		}
		if got := headers.Get("User-Agent"); got != wantUA {
			t.Errorf("%s User-Agent = %q, want %q like /responses", path, got, wantUA)
		}
		if got := headers.Get("Originator"); got != wantOriginator {
			t.Errorf("%s Originator = %q, want %q like /responses", path, got, wantOriginator)
		}
	}
}

func TestCodexPollQuotaUsesHeaderDefaultsUserAgentWithoutCloaking(t *testing.T) {
	cfg := &config.Config{}
	cfg.Codex.DisableCodexCloaking = true
	cfg.CodexHeaderDefaults.UserAgent = "my-agent/1.0"
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"access_token": "token", "account_id": "acct"},
	}
	wantUA, wantOriginator := codexResponsesIdentity(t, cfg, auth)
	if wantUA != "my-agent/1.0" {
		t.Fatalf("/responses User-Agent = %q, want the configured my-agent/1.0", wantUA)
	}
	seen := pollCodexQuotaHeaders(t, cfg, auth)
	for _, path := range []string{"/backend-api/wham/usage", "/backend-api/subscriptions"} {
		headers := seen[path]
		if headers == nil {
			t.Fatalf("request %s not made; saw %v", path, seen)
		}
		if got := headers.Get("User-Agent"); got != wantUA {
			t.Errorf("%s User-Agent = %q, want %q like /responses", path, got, wantUA)
		}
		if got := headers.Get("Originator"); got != wantOriginator {
			t.Errorf("%s Originator = %q, want %q like /responses", path, got, wantOriginator)
		}
	}
}

// The ChatGPT backend usage endpoints take the outer OAuth token. A SIWC
// credential must not switch the poll to its nested token, even when the
// configured User-Agent is not a Codex client User-Agent.
func TestCodexPollQuotaSendsOuterOAuthTokenForSIWCCredential(t *testing.T) {
	cfg := &config.Config{}
	cfg.Codex.DisableCodexCloaking = true
	cfg.CodexHeaderDefaults.UserAgent = "my-agent/1.0"
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{
			"access_token": "outer-token",
			"account_id":   "acct",
			"use_siwc":     true,
			"siwc":         map[string]any{"access_token": "siwc-token"},
		},
	}
	seen := pollCodexQuotaHeaders(t, cfg, auth)
	for _, path := range []string{"/backend-api/wham/usage", "/backend-api/subscriptions", "/backend-api/wham/rate-limit-reset-credits"} {
		headers := seen[path]
		if headers == nil {
			t.Fatalf("request %s not made; saw %v", path, seen)
		}
		if got := headers.Get("Authorization"); got != "Bearer outer-token" {
			t.Errorf("%s Authorization = %q, want Bearer outer-token", path, got)
		}
	}
}
