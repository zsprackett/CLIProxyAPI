package executor

import (
	"strings"
	"testing"

	"github.com/MoeForge/moego/ojson"
	claudeauth "github.com/router-for-me/CLIProxyAPI/v8/internal/auth/claude"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

// classifierPayload mirrors the auto-mode classifier request that Claude Code
// 2.1.304 sends: its own system prompt (no CLI identity block), and only user
// turns. messages.0 (CLAUDE.md) is stable; messages.1 (the transcript) changes
// on every call.
func classifierPayload(systemPrompt string) []byte {
	return []byte(`{"model":"claude-sonnet-5","max_tokens":64,"thinking":{"type":"disabled"},` +
		`"system":[` +
		`{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.304.a80; cc_entrypoint=sdk-ts;"},` +
		`{"type":"text","text":"` + systemPrompt + `","cache_control":{"type":"ephemeral","ttl":"1h"}},` +
		`{"type":"text","text":"## Session Context"}],` +
		`"messages":[` +
		`{"role":"user","content":[{"type":"text","text":"CLAUDE.md contents","cache_control":{"type":"ephemeral","ttl":"1h"}}]},` +
		`{"role":"user","content":[{"type":"text","text":"<transcript>tool call 42</transcript>","cache_control":{"type":"ephemeral","ttl":"1h"}}]}],` +
		`"metadata":{"user_id":"{\"device_id\":\"0000000000000000000000000000000000000000000000000000000000000000\",\"account_uuid\":\"\",\"session_id\":\"11111111-2222-4333-8444-555555555555\"}"}}`)
}

const classifierSystemPrompt = "You are a security monitor for autonomous AI coding agents. Judge the action."

func sendClassifierRequest(t *testing.T, stream bool, payload []byte) []byte {
	t.Helper()
	return sendClassifierRequestWithAuth(t, stream, payload, midSystemAuth(), midSystemConfig())
}

func classifierOAuth() (*cliproxyauth.Auth, *config.Config) {
	return &cliproxyauth.Auth{
		ID:         "classifier-oauth",
		Attributes: map[string]string{"api_key": "sk-ant-oat01-classifier-test", "cloak_mode": "always"},
		Metadata: map[string]any{
			"account_uuid": "11111111-2222-4333-8444-555555555555",
			claudeauth.ClaudeDeviceIDsMetadataKey: []string{
				"0000000000000000000000000000000000000000000000000000000000000001",
			},
		},
	}, &config.Config{}
}

func sendClassifierRequestWithAuth(t *testing.T, stream bool, payload []byte, auth *cliproxyauth.Auth, cfg *config.Config) []byte {
	t.Helper()
	upstream := &midSystemUpstream{}
	ex := NewClaudeExecutor(cfg)
	ctx := upstream.context(t, nil)
	if stream {
		result, err := ex.ExecuteStream(ctx, auth, cliproxyexecutor.Request{
			Model: "claude-sonnet-5", Payload: payload,
		}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude})
		if err != nil {
			t.Fatalf("ExecuteStream() error = %v", err)
		}
		for chunk := range result.Chunks {
			if chunk.Err != nil {
				t.Fatalf("stream chunk error = %v", chunk.Err)
			}
		}
	} else if _, err := ex.Execute(ctx, auth, cliproxyexecutor.Request{
		Model: "claude-sonnet-5", Payload: payload,
	}, cliproxyexecutor.Options{SourceFormat: sdktranslator.FormatClaude}); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !upstream.called {
		t.Fatal("expected the request to reach the upstream")
	}
	return upstream.body
}

// When a cloaked caller sends the auto-mode classifier request, the upstream
// receives the classifier's own system prompt at the top level, the way Claude
// Code sends it. Moving it after the transcript made every call rewrite the
// whole prompt to cache and read nothing back.
func TestClaudeExecutor_CloakedClassifierKeepsItsSystemPrompt(t *testing.T) {
	for _, stream := range []bool{false, true} {
		name := "execute"
		if stream {
			name = "execute stream"
		}
		t.Run(name, func(t *testing.T) {
			body := sendClassifierRequest(t, stream, classifierPayload(classifierSystemPrompt))

			system := ojson.GetBytes(body, "system").Array()
			if len(system) != 3 {
				t.Fatalf("system has %d blocks, want billing + classifier prompt + session context; system=%s", len(system), ojson.GetBytes(body, "system").Raw)
			}
			if got := system[0].Get("text").String(); !strings.HasPrefix(got, "x-anthropic-billing-header:") || !strings.Contains(got, "cc_entrypoint=cli;") {
				t.Fatalf("system[0] = %q, want the cloaked CLI billing header", got)
			}
			if got := system[1].Get("text").String(); got != classifierSystemPrompt {
				t.Fatalf("system[1] = %q, want the classifier prompt", got)
			}
			if got := system[1].Get("cache_control.ttl").String(); got != "1h" {
				t.Fatalf("system[1] cache ttl = %q, want 1h", got)
			}
			if got := system[2].Get("text").String(); got != "## Session Context" {
				t.Fatalf("system[2] = %q, want the session context", got)
			}

			messages := ojson.GetBytes(body, "messages").Array()
			if len(messages) != 2 {
				t.Fatalf("messages has %d turns, want the caller's 2 user turns; messages=%s", len(messages), ojson.GetBytes(body, "messages").Raw)
			}
			if got := messages[0].Get("content").Raw; got != `[{"type":"text","text":"CLAUDE.md contents","cache_control":{"type":"ephemeral","ttl":"1h"}}]` {
				t.Fatalf("messages.0.content = %s, want the caller's cached CLAUDE.md turn unchanged", got)
			}
			if got := messages[1].Get("content.0.text").String(); got != "<transcript>tool call 42</transcript>" {
				t.Fatalf("messages.1 text = %q, want the transcript", got)
			}
		})
	}
}

// A caller system prompt that is not the classifier still gets the CLI
// identity block. With only user turns, the cloak keeps the caller prompt at
// the top level after the identity block.
func TestClaudeExecutor_CloakedNonClassifierStillGetsIdentityBlock(t *testing.T) {
	body := sendClassifierRequest(t, false, classifierPayload("You are a helpful reviewer."))

	if got := ojson.GetBytes(body, "system.1.text").String(); got != claudeCodeCLIIdentity {
		t.Fatalf("system.1 = %q, want the CLI identity block", got)
	}
	if got := ojson.GetBytes(body, "system.2.text").String(); got != "You are a helpful reviewer." {
		t.Fatalf("system.2 = %q, want the caller prompt after the identity block; system=%s", got, ojson.GetBytes(body, "system").Raw)
	}
}

func classifierModes() []struct {
	name   string
	stream bool
} {
	return []struct {
		name   string
		stream bool
	}{{"execute", false}, {"execute stream", true}}
}

// A genuine classifier request on an OAuth credential keeps its system prompt
// at the top level, the same as on an API key.
func TestClaudeExecutor_OAuthClassifierKeepsItsSystemPrompt(t *testing.T) {
	for _, mode := range classifierModes() {
		t.Run(mode.name, func(t *testing.T) {
			auth, cfg := classifierOAuth()
			body := sendClassifierRequestWithAuth(t, mode.stream, classifierPayload(classifierSystemPrompt), auth, cfg)

			if got := ojson.GetBytes(body, "system.1.text").String(); got != classifierSystemPrompt {
				t.Fatalf("system.1 = %q, want the classifier prompt; system=%s", got, ojson.GetBytes(body, "system").Raw)
			}
			if got := ojson.GetBytes(body, "system.1.cache_control.ttl").String(); got != "1h" {
				t.Fatalf("system.1 cache ttl = %q, want 1h", got)
			}
		})
	}
}

// A tool-enabled request that forces a tool call is not the classifier, even
// when its system prompt opens with the classifier sentence. On an OAuth
// credential it keeps the ordinary cloaked shape: the CLI identity block at
// the top level and the caller prompt relocated out of it.
func TestClaudeExecutor_OAuthToolRequestWithClassifierPrefixIsRelocated(t *testing.T) {
	payload := []byte(`{"model":"claude-sonnet-5","max_tokens":4096,` +
		`"system":[{"type":"text","text":"` + classifierSystemPrompt + ` Then call inspect."}],` +
		`"tools":[{"name":"inspect","description":"Inspect a file.","input_schema":{"type":"object","properties":{"path":{"type":"string"}}}}],` +
		`"tool_choice":{"type":"tool","name":"inspect"},` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"Check main.go"}]}]}`)
	for _, mode := range classifierModes() {
		t.Run(mode.name, func(t *testing.T) {
			auth, cfg := classifierOAuth()
			body := sendClassifierRequestWithAuth(t, mode.stream, payload, auth, cfg)

			if got := ojson.GetBytes(body, "system.1.text").String(); got != claudeCodeCLIIdentity {
				t.Fatalf("system.1 = %q, want the CLI identity block; system=%s", got, ojson.GetBytes(body, "system").Raw)
			}
			if raw := ojson.GetBytes(body, "system").Raw; strings.Contains(raw, "security monitor") {
				t.Fatalf("system = %s, want the caller prompt relocated out of the top-level system", raw)
			}
			if raw := ojson.GetBytes(body, "messages").Raw; !strings.Contains(raw, "security monitor") {
				t.Fatalf("messages = %s, want the relocated caller prompt", raw)
			}
		})
	}
}

// A malformed cache_control on a classifier system block must not reach the
// upstream, while a valid breakpoint on the classifier prompt is kept.
func TestClaudeExecutor_ClassifierDropsMalformedCacheControl(t *testing.T) {
	payload := []byte(`{"model":"claude-sonnet-5","max_tokens":64,"thinking":{"type":"disabled"},` +
		`"system":[` +
		`{"type":"text","text":"` + classifierSystemPrompt + `","cache_control":{"type":"ephemeral","ttl":"1h"}},` +
		`{"type":"text","text":"## Session Context","cache_control":"not-an-object"}],` +
		`"messages":[{"role":"user","content":[{"type":"text","text":"<transcript>tool call 42</transcript>"}]}]}`)
	for _, mode := range classifierModes() {
		t.Run(mode.name, func(t *testing.T) {
			auth, cfg := classifierOAuth()
			body := sendClassifierRequestWithAuth(t, mode.stream, payload, auth, cfg)

			if got := ojson.GetBytes(body, "system.1.cache_control.ttl").String(); got != "1h" {
				t.Fatalf("system.1 cache ttl = %q, want the valid breakpoint kept; system=%s", got, ojson.GetBytes(body, "system").Raw)
			}
			if got := ojson.GetBytes(body, "system.2.text").String(); got != "## Session Context" {
				t.Fatalf("system.2 = %q, want the session context", got)
			}
			if strings.Contains(string(body), "not-an-object") {
				t.Fatalf("upstream body carries the malformed cache_control: %s", ojson.GetBytes(body, "system").Raw)
			}
		})
	}
}
