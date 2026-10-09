package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	xaiBillingWeeklyURL  = "https://cli-chat-proxy.grok.com/v1/billing?format=credits"
	xaiBillingMonthlyURL = "https://cli-chat-proxy.grok.com/v1/billing"
)

// PollQuota fetches a Grok CLI credential's billing usage without consuming
// quota and reports it as x-xai-billing-* headers. Credentials on the official
// API path (API keys) have no billing endpoint and are skipped.
func (e *XAIExecutor) PollQuota(ctx context.Context, auth *cliproxyauth.Auth) (http.Header, error) {
	if e == nil || auth == nil || xaiUsingAPI(auth) {
		return nil, nil
	}
	if token, _ := xaiCreds(auth); strings.TrimSpace(token) == "" {
		return nil, nil
	}
	weekly, errWeekly := e.fetchXAIBilling(ctx, auth, xaiBillingWeeklyURL)
	monthly, errMonthly := e.fetchXAIBilling(ctx, auth, xaiBillingMonthlyURL)
	if errWeekly != nil && errMonthly != nil {
		return nil, errWeekly
	}
	return helps.XAIBillingQuotaHeaders(weekly, monthly, time.Now()), nil
}

func (e *XAIExecutor) fetchXAIBilling(ctx context.Context, auth *cliproxyauth.Auth, url string) ([]byte, error) {
	req, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if errRequest != nil {
		return nil, fmt.Errorf("create xai billing request: %w", errRequest)
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("User-Agent", "xai-grok-workspace/"+xaiClientVersionValue)
	req.Header.Set(xaiTokenAuthHeader, xaiTokenAuthValue)
	req.Header.Set(xaiClientVersionHeader, xaiClientVersionValue)
	req.Header.Set(xaiClientIdentifierHeader, xaiClientIdentifierValue)
	if userID := xaiMetadataString(auth.Metadata, "sub"); userID != "" {
		req.Header.Set("x-userid", userID)
	}
	resp, errDo := e.HttpRequest(ctx, auth, req)
	if errDo != nil {
		return nil, fmt.Errorf("fetch xai billing: %w", errDo)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("failed to close xai billing response body: %v", errClose)
		}
	}()
	body, errRead := io.ReadAll(resp.Body)
	if errRead != nil {
		return nil, fmt.Errorf("read xai billing response: %w", errRead)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("fetch xai billing failed with status %d", resp.StatusCode)
	}
	return body, nil
}

// PollQuota delegates quota polling to the HTTP executor.
func (e *XAIAutoExecutor) PollQuota(ctx context.Context, auth *cliproxyauth.Auth) (http.Header, error) {
	if e == nil || e.httpExec == nil {
		return nil, nil
	}
	return e.httpExec.PollQuota(ctx, auth)
}
