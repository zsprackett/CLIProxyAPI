package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const codexUsageURL = "https://chatgpt.com/backend-api/wham/usage"

// PollQuota fetches a ChatGPT-login credential's rate-limit usage without
// consuming quota and reports it as the x-codex-* headers live responses carry.
// API keys have no usage endpoint and are skipped.
func (e *CodexExecutor) PollQuota(ctx context.Context, auth *cliproxyauth.Auth) (http.Header, error) {
	if e == nil || auth == nil || codexAuthUsesAPIKey(auth) {
		return nil, nil
	}
	if token, _ := codexCreds(auth); strings.TrimSpace(token) == "" {
		return nil, nil
	}
	req, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, codexUsageURL, nil)
	if errRequest != nil {
		return nil, fmt.Errorf("create codex usage request: %w", errRequest)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", codexUserAgent)
	req.Header.Set("Originator", codexOriginator)
	if accountID, ok := auth.Metadata["account_id"].(string); ok && strings.TrimSpace(accountID) != "" {
		req.Header.Set("Chatgpt-Account-Id", accountID)
	}
	resp, errDo := e.HttpRequest(ctx, auth, req)
	if errDo != nil {
		return nil, fmt.Errorf("fetch codex usage: %w", errDo)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("failed to close codex usage response body: %v", errClose)
		}
	}()
	body, errRead := io.ReadAll(resp.Body)
	if errRead != nil {
		return nil, fmt.Errorf("read codex usage response: %w", errRead)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("fetch codex usage failed with status %d", resp.StatusCode)
	}
	return helps.CodexUsageQuotaHeaders(body), nil
}

// PollQuota delegates quota polling to the HTTP executor.
func (e *CodexAutoExecutor) PollQuota(ctx context.Context, auth *cliproxyauth.Auth) (http.Header, error) {
	if e == nil || e.httpExec == nil {
		return nil, nil
	}
	return e.httpExec.PollQuota(ctx, auth)
}
