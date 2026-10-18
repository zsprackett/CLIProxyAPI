package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

// Claude Code sends "(external, cli, workload/cron)" on turns fired by a scheduled
// task. Those turns must reach upstream as native Claude Code, the same as the
// interactive turns of the session. A cloaked cron turn renames every tool, so it
// cannot read the prompt cache of the session and writes the full context again.
func TestClaudeExecutor_WorkloadUserAgentKeepsNativePassthrough(t *testing.T) {
	const userID = `{"device_id":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","account_uuid":"","session_id":"11111111-2222-4333-8444-555555555555"}`
	// The proxy signs a native billing header by inserting " cch=<5 hex>;".
	proxySignature := regexp.MustCompile(` cch=[0-9a-f]{5};`)

	tests := []struct {
		name      string
		userAgent string
		billing   string
		native    bool
	}{
		{
			name:      "interactive turn",
			userAgent: "claude-cli/2.1.302 (external, cli)",
			billing:   "x-anthropic-billing-header: cc_version=2.1.302.258; cc_entrypoint=cli;",
			native:    true,
		},
		{
			name:      "cron turn",
			userAgent: "claude-cli/2.1.302 (external, cli, workload/cron)",
			billing:   "x-anthropic-billing-header: cc_version=2.1.302.258; cc_entrypoint=cli; cc_workload=cron;",
			native:    true,
		},
		{
			name:      "unknown suffix",
			userAgent: "claude-cli/2.1.302 (external, cli, foo/bar)",
			billing:   "x-anthropic-billing-header: cc_version=2.1.302.258; cc_entrypoint=cli;",
			native:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload := []byte(`{"model":"claude-opus-4-6","system":[{"type":"text","text":"` + tt.billing + `"},{"type":"text","text":"sys"}],` +
				`"tools":[{"name":"Agent","description":"Launch an agent.","input_schema":{"type":"object","properties":{}}}],` +
				`"messages":[{"role":"user","content":"hi"}],"metadata":{"user_id":` + fmt.Sprintf("%q", userID) + `}}`)
			var seenBody []byte
			var seenUserAgent string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seenBody, _ = io.ReadAll(r.Body)
				seenUserAgent = r.Header.Get("User-Agent")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","model":"claude-opus-4-6","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`))
			}))
			defer server.Close()

			incoming := http.Header{
				"User-Agent":               {tt.userAgent},
				"X-App":                    {"cli"},
				"Anthropic-Beta":           {"claude-code-20250219"},
				"X-Claude-Code-Session-Id": {"11111111-2222-4333-8444-555555555555"},
			}
			auth := &cliproxyauth.Auth{ID: "workload-user-agent", Metadata: claudeOAuthTestMetadata(), Attributes: map[string]string{
				"api_key": "sk-ant-oat-workload-user-agent", "base_url": server.URL,
			}}
			req := cliproxyexecutor.Request{Model: "claude-opus-4-6", Payload: payload}
			opts := cliproxyexecutor.Options{
				SourceFormat:    sdktranslator.FormatClaude,
				OriginalRequest: payload,
				Headers:         incoming,
			}
			if _, errExecute := NewClaudeExecutor(&config.Config{}).Execute(context.Background(), auth, req, opts); errExecute != nil {
				t.Fatalf("Execute() error = %v", errExecute)
			}

			toolName := gjson.GetBytes(seenBody, "tools.0.name").String()
			billingText := gjson.GetBytes(seenBody, "system.0.text").String()
			if tt.native {
				if seenUserAgent != tt.userAgent {
					t.Errorf("upstream User-Agent = %q, want %q", seenUserAgent, tt.userAgent)
				}
				if toolName != "Agent" {
					t.Errorf("upstream tool name = %q, want %q", toolName, "Agent")
				}
				if unsigned := proxySignature.ReplaceAllString(billingText, ""); unsigned != tt.billing {
					t.Errorf("upstream billing header = %q, want %q plus the proxy cch", billingText, tt.billing)
				}
				return
			}
			if seenUserAgent != "claude-cli/2.1.280 (external, cli)" {
				t.Errorf("upstream User-Agent = %q, want the cloaked %q", seenUserAgent, "claude-cli/2.1.280 (external, cli)")
			}
			if !strings.HasPrefix(toolName, "mcp__") || !strings.HasSuffix(toolName, "_Agent") {
				t.Errorf("upstream tool name = %q, want a cloaked mcp__..._Agent alias", toolName)
			}
		})
	}
}
