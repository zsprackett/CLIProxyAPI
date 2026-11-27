package executor

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestCodexPollQuotaPollsSIWCCredentialWithOnlyOuterToken(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{
			"access_token": "outer-token",
			"account_id":   "acct",
			"use_siwc":     true,
		},
	}
	seen := pollCodexQuotaHeaders(t, &config.Config{}, auth)
	headers := seen["/backend-api/wham/usage"]
	if headers == nil {
		t.Fatalf("usage request not made; saw %v", seen)
	}
	if got := headers.Get("Authorization"); got != "Bearer outer-token" {
		t.Fatalf("Authorization = %q, want Bearer outer-token", got)
	}
}

func TestCodexPollQuotaSkipsSIWCCredentialWithoutOuterToken(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{
			"account_id": "acct",
			"use_siwc":   true,
			"siwc":       map[string]any{"access_token": "siwc-token"},
		},
	}
	var requests atomic.Int32
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		requests.Add(1)
		return nil, errors.New("unexpected request")
	})
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", http.RoundTripper(transport))
	_, errPoll := NewCodexExecutor(&config.Config{}).PollQuota(ctx, auth, cliproxyauth.PollOnDemand)
	if !errors.Is(errPoll, cliproxyauth.ErrQuotaNotPollable) {
		t.Fatalf("PollQuota() error = %v, want ErrQuotaNotPollable", errPoll)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("PollQuota() made %d requests, want 0", n)
	}
}
