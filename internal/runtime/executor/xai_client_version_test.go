package executor

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// xaiVersionAtLeast compares dotted numeric version strings.
func xaiVersionAtLeast(got, floor string) bool {
	gotParts := strings.Split(got, ".")
	floorParts := strings.Split(floor, ".")
	for i := 0; i < len(gotParts) || i < len(floorParts); i++ {
		var g, f int
		if i < len(gotParts) {
			g, _ = strconv.Atoi(gotParts[i])
		}
		if i < len(floorParts) {
			f, _ = strconv.Atoi(floorParts[i])
		}
		if g != f {
			return g > f
		}
	}
	return true
}

func TestXAIChatProxyClientVersionMeetsServerFloor(t *testing.T) {
	// cli-chat-proxy.grok.com started rejecting client versions older than
	// 1.0.13 with HTTP 426 ("Your Grok CLI version is outdated"); see #6249.
	const serverFloor = "1.0.13"
	if !xaiVersionAtLeast(xaiClientVersionValue, serverFloor) {
		t.Fatalf("xaiClientVersionValue = %q, cli-chat-proxy rejects clients older than %s with 426", xaiClientVersionValue, serverFloor)
	}
}

func TestXAIChatProxyIdentityHeadersDeriveFromActiveVersion(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Provider:   "xai",
		Attributes: map[string]string{"auth_kind": "oauth"},
	}
	req, err := http.NewRequest(http.MethodPost, xaiChatBaseURL(auth)+"/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	applyXAIChatHeaders(req, auth, "token", false, "")

	version := xaiClientVersion()
	if got := req.Header.Get(xaiClientVersionHeader); got != version {
		t.Fatalf("%s = %q, want %q", xaiClientVersionHeader, got, version)
	}
	if got, want := req.Header.Get("User-Agent"), "xai-grok-workspace/"+version; got != want {
		t.Fatalf("User-Agent = %q, want %q (must derive from xaiClientVersion())", got, want)
	}
}

func TestXAIChatProxyCustomHeadersOverridePinnedVersion(t *testing.T) {
	auth := &cliproxyauth.Auth{
		Provider: "xai",
		Attributes: map[string]string{
			"auth_kind":                    "oauth",
			"header:x-grok-client-version": "9.9.9",
		},
	}
	req, err := http.NewRequest(http.MethodPost, xaiChatBaseURL(auth)+"/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	applyXAIChatHeaders(req, auth, "token", false, "")

	if got := req.Header.Get(xaiClientVersionHeader); got != "9.9.9" {
		t.Fatalf("%s = %q, want the per-auth custom header to override the pin", xaiClientVersionHeader, got)
	}
}

func TestXAIChatProxyIdentityHeadersFollowDynamicVersion(t *testing.T) {
	restore := helps.SetXAIClientVersionForTest("1.2.34")
	defer restore()

	auth := &cliproxyauth.Auth{
		Provider:   "xai",
		Attributes: map[string]string{"auth_kind": "oauth"},
	}
	req, err := http.NewRequest(http.MethodPost, xaiChatBaseURL(auth)+"/chat/completions", nil)
	if err != nil {
		t.Fatal(err)
	}
	applyXAIChatHeaders(req, auth, "token", false, "")

	if got := req.Header.Get(xaiClientVersionHeader); got != "1.2.34" {
		t.Fatalf("%s = %q, want dynamic version 1.2.34", xaiClientVersionHeader, got)
	}
	if got, want := req.Header.Get("User-Agent"), "xai-grok-workspace/1.2.34"; got != want {
		t.Fatalf("User-Agent = %q, want %q", got, want)
	}
}

func pollXAIBillingHeaders(t *testing.T, auth *cliproxyauth.Auth) []http.Header {
	t.Helper()
	var mu sync.Mutex
	var seen []http.Header
	transport := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		mu.Lock()
		seen = append(seen, req.Header.Clone())
		mu.Unlock()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(xaiBillingMonthlyNoLimitBody)),
			Request:    req,
		}, nil
	})
	ctx := context.WithValue(context.Background(), "cliproxy.roundtripper", http.RoundTripper(transport))
	if _, errPoll := NewXAIExecutor(&config.Config{}).PollQuota(ctx, auth, cliproxyauth.PollOnDemand); errPoll != nil {
		t.Fatalf("PollQuota() error = %v", errPoll)
	}
	if len(seen) == 0 {
		t.Fatal("PollQuota() made no billing request")
	}
	return seen
}

// The billing poll goes to the same cli-chat-proxy host as chat, so it must
// send the same identity headers, including the npm-refreshed client version.
func TestXAIPollQuotaSendsChatProxyIdentityHeaders(t *testing.T) {
	restore := helps.SetXAIClientVersionForTest("1.2.34")
	defer restore()

	auth := &cliproxyauth.Auth{
		Provider: "xai",
		Metadata: map[string]any{"access_token": "token", "auth_kind": "oauth"},
	}
	chatReq, errReq := http.NewRequest(http.MethodPost, xaiChatBaseURL(auth)+"/chat/completions", nil)
	if errReq != nil {
		t.Fatal(errReq)
	}
	applyXAIChatHeaders(chatReq, auth, "token", false, "")

	for i, headers := range pollXAIBillingHeaders(t, auth) {
		if got := headers.Get(xaiClientVersionHeader); got != "1.2.34" {
			t.Errorf("billing request %d %s = %q, want dynamic version 1.2.34", i, xaiClientVersionHeader, got)
		}
		if got, want := headers.Get("User-Agent"), "xai-grok-workspace/1.2.34"; got != want {
			t.Errorf("billing request %d User-Agent = %q, want %q", i, got, want)
		}
		for _, name := range []string{xaiTokenAuthHeader, xaiClientIdentifierHeader, xaiAuthenticateResponseHeader} {
			if got, want := headers.Get(name), chatReq.Header.Get(name); got != want {
				t.Errorf("billing request %d %s = %q, want %q like chat", i, name, got, want)
			}
		}
	}
}
