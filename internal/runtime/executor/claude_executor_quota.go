package executor

import (
	"context"
	"net/http"
	"strings"

	claudeauth "github.com/router-for-me/CLIProxyAPI/v8/internal/auth/claude"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// PollQuota fetches an OAuth credential's rate-limit usage without consuming
// quota and reports it as the unified rate-limit headers live responses carry.
// API keys and setup tokens have no usage endpoint and are skipped.
func (e *ClaudeExecutor) PollQuota(ctx context.Context, auth *cliproxyauth.Auth) (http.Header, error) {
	if e == nil || auth == nil || auth.AuthKind() == cliproxyauth.AuthKindAPIKey {
		return nil, nil
	}
	if auth.Attributes != nil && strings.TrimSpace(auth.Attributes["api_key"]) != "" {
		return nil, nil
	}
	accessToken, _ := claudeCreds(auth)
	if strings.TrimSpace(accessToken) == "" || isClaudeSetupToken(auth, accessToken) {
		return nil, nil
	}
	svc := claudeauth.NewClaudeAuthWithProxyURL(e.cfg, auth.ProxyURL)
	body, errFetch := svc.FetchOAuthUsage(ctx, accessToken)
	if errFetch != nil {
		return nil, errFetch
	}
	return helps.ClaudeUsageQuotaHeaders(body), nil
}
