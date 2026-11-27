package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MoeForge/moego/ojson"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

const mainCacheTTLTestUserID = `{"device_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","account_uuid":"","session_id":"11111111-2222-4333-8444-555555555555"}`

// runMainCacheTTLNativeOAuth sends a confirmed native Claude Code request on an
// OAuth credential and returns the body and headers that reached upstream.
func runMainCacheTTLNativeOAuth(t *testing.T, cfg *config.Config, authAttrs map[string]string, payload []byte, stream bool) ([]byte, http.Header, error) {
	t.Helper()
	var seenBody []byte
	var seenHeader http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenBody, _ = io.ReadAll(r.Body)
		seenHeader = r.Header.Clone()
		if stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: message_stop\n" + `data: {"type":"message_stop"}` + "\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-opus-4-6","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer server.Close()

	incoming := http.Header{
		"User-Agent":               {"claude-cli/2.1.321 (external, cli)"},
		"X-App":                    {"cli"},
		"Anthropic-Beta":           {"claude-code-20250219"},
		"X-Claude-Code-Session-Id": {"11111111-2222-4333-8444-555555555555"},
	}
	if !helps.DetectClaudeCodeRequest(incoming, payload, false).Confirmed {
		t.Fatal("fixture must be classified as a native Claude Code request")
	}
	attrs := map[string]string{"api_key": "sk-ant-oat-main-cache-ttl", "base_url": server.URL}
	for name, value := range authAttrs {
		attrs[name] = value
	}
	auth := &cliproxyauth.Auth{ID: "main-cache-ttl-policy", Metadata: claudeOAuthTestMetadata(), Attributes: attrs}
	executor := NewClaudeExecutor(cfg)
	req := cliproxyexecutor.Request{Model: "claude-opus-4-6", Payload: payload}
	opts := cliproxyexecutor.Options{
		Stream:          stream,
		SourceFormat:    sdktranslator.FormatClaude,
		OriginalRequest: payload,
		Headers:         incoming,
	}
	if stream {
		result, errStream := executor.ExecuteStream(context.Background(), auth, req, opts)
		if errStream != nil {
			return seenBody, seenHeader, errStream
		}
		for chunk := range result.Chunks {
			if chunk.Err != nil {
				return seenBody, seenHeader, chunk.Err
			}
		}
		return seenBody, seenHeader, nil
	}
	_, errExecute := executor.Execute(context.Background(), auth, req, opts)
	return seenBody, seenHeader, errExecute
}

func assertMainCacheTTLMarkersWithoutTTL(t *testing.T, body []byte) {
	t.Helper()
	markers := 0
	forEachClaudeCacheControlBlock(body, func(path string, block ojson.Result) {
		cacheControl := block.Get("cache_control")
		if !cacheControl.IsObject() {
			return
		}
		markers++
		if ttl := cacheControl.Get("ttl"); ttl.Exists() {
			t.Errorf("%s ttl = %s, want absent; body=%s", path, ttl.Raw, body)
		}
	})
	if markers == 0 {
		t.Fatalf("no cache_control markers reached upstream: %s", body)
	}
}

func TestClaudeExecutor_MainCacheTTL1hSkipsWhenExtendedCacheBetaNotSent(t *testing.T) {
	payload := []byte(`{"model":"claude-opus-4-6","thread":{"type":"create"},"system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}],"metadata":{"user_id":` + fmt.Sprintf("%q", mainCacheTTLTestUserID) + `}}`)
	policies := []struct {
		name      string
		strip     []string
		authAttrs map[string]string
	}{
		{name: "header-defaults strip", strip: []string{claudeExtendedCacheTTLBeta}},
		{name: "auth header override", authAttrs: map[string]string{"header:Anthropic-Beta": "claude-code-20250219"}},
	}
	for _, policy := range policies {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", policy.name, stream), func(t *testing.T) {
				cfg := &config.Config{}
				cfg.ClaudeCode.MainCacheTTL1h = true
				cfg.ClaudeHeaderDefaults.AnthropicBetaStrip = policy.strip
				body, header, err := runMainCacheTTLNativeOAuth(t, cfg, policy.authAttrs, payload, stream)
				if err != nil {
					t.Fatalf("request error = %v", err)
				}
				if claudeRequestedBetas(header.Get("Anthropic-Beta"), nil)[claudeExtendedCacheTTLBeta] {
					t.Fatalf("operator header policy was overridden: Anthropic-Beta = %q", header.Get("Anthropic-Beta"))
				}
				assertMainCacheTTLMarkersWithoutTTL(t, body)
			})
		}
	}
}

func TestClaudeExecutor_MainCacheTTL1hPayloadFilterRemovesTTL(t *testing.T) {
	payload := []byte(`{"model":"claude-opus-4-6","system":[{"type":"text","text":"sys","cache_control":{"type":"ephemeral"}}],"messages":[{"role":"user","content":[{"type":"text","text":"hi","cache_control":{"type":"ephemeral"}}]}],"metadata":{"user_id":` + fmt.Sprintf("%q", mainCacheTTLTestUserID) + `}}`)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			cfg := &config.Config{}
			cfg.ClaudeCode.MainCacheTTL1h = true
			cfg.Payload.Filter = []config.PayloadFilterRule{{
				Models: []config.PayloadModelRule{{Name: "claude-opus-4-6", Protocol: "claude"}},
				Params: []string{"system.1.cache_control.ttl", "messages.0.content.0.cache_control.ttl"},
			}}
			body, _, err := runMainCacheTTLNativeOAuth(t, cfg, nil, payload, stream)
			if err != nil {
				t.Fatalf("request error = %v", err)
			}
			assertMainCacheTTLMarkersWithoutTTL(t, body)
		})
	}
}
