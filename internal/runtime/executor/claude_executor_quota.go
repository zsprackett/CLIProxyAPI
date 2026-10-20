package executor

import (
	"context"
	"strings"
	"sync"
	"time"

	claudeauth "github.com/router-for-me/CLIProxyAPI/v8/internal/auth/claude"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

// PollQuota fetches an OAuth credential's usage and profile without consuming
// quota. The two requests run independently, so a profile failure keeps the
// windows and only marks the plan section failed. API keys and setup tokens
// have no usage endpoint.
func (e *ClaudeExecutor) PollQuota(ctx context.Context, auth *cliproxyauth.Auth, _ cliproxyauth.PollTrigger) (cliproxyauth.QuotaObservation, error) {
	if e == nil || auth == nil || auth.AuthKind() == cliproxyauth.AuthKindAPIKey {
		return cliproxyauth.QuotaObservation{}, cliproxyauth.ErrQuotaNotPollable
	}
	if auth.Attributes != nil && strings.TrimSpace(auth.Attributes["api_key"]) != "" {
		return cliproxyauth.QuotaObservation{}, cliproxyauth.ErrQuotaNotPollable
	}
	accessToken, _ := claudeCreds(auth)
	if strings.TrimSpace(accessToken) == "" || isClaudeSetupToken(auth, accessToken) {
		return cliproxyauth.QuotaObservation{}, cliproxyauth.ErrQuotaNotPollable
	}
	svc := claudeauth.NewClaudeAuthWithProxyURL(e.cfg, auth.ProxyURL)
	var src helps.ClaudeQuotaSources
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		src.Usage, src.UsageErr = svc.FetchOAuthUsage(ctx, accessToken)
	}()
	go func() {
		defer wg.Done()
		src.Profile, src.ProfileErr = svc.FetchOAuthProfile(ctx, accessToken)
	}()
	wg.Wait()
	return helps.ClaudeQuotaObservation(src, time.Now())
}
