package executor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/MoeForge/moego/json"
	"github.com/MoeForge/moego/ojson"
	codexauth "github.com/router-for-me/CLIProxyAPI/v8/internal/auth/codex"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	codexUsageURL        = "https://chatgpt.com/backend-api/wham/usage"
	codexSubscriptionURL = "https://chatgpt.com/backend-api/subscriptions"
	codexResetCreditsURL = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits"
)

// PollQuota fetches a ChatGPT-login credential's usage, subscription, and
// reset credits without consuming quota. The three requests run independently
// and each one owns its sections. API keys have no usage endpoint.
func (e *CodexExecutor) PollQuota(ctx context.Context, auth *cliproxyauth.Auth, _ cliproxyauth.PollTrigger) (cliproxyauth.QuotaObservation, error) {
	if e == nil || auth == nil || codexAuthUsesAPIKey(auth) {
		return cliproxyauth.QuotaObservation{}, cliproxyauth.ErrQuotaNotPollable
	}
	// Check the token the fetch sends: the outer OAuth token, even for SIWC.
	if token, _ := codexCredsForRequest(cliproxyauth.WithCodexClientRequest(ctx, true), auth); strings.TrimSpace(token) == "" {
		return cliproxyauth.QuotaObservation{}, cliproxyauth.ErrQuotaNotPollable
	}
	accountID := codexQuotaAccountID(auth)
	src := helps.CodexQuotaSources{Credential: codexQuotaCredential(auth)}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		src.Usage, src.UsageErr = e.fetchCodexQuotaJSON(ctx, auth, codexUsageURL, accountID, nil)
	}()
	go func() {
		defer wg.Done()
		src.ResetCredits, src.ResetCreditsErr = e.fetchCodexQuotaJSON(ctx, auth, codexResetCreditsURL, accountID, map[string]string{
			"OpenAI-Beta": "codex-1",
			"Originator":  "Codex Desktop",
		})
	}()
	if accountID != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			src.Subscription, src.SubscriptionErr = e.fetchCodexQuotaJSON(ctx, auth,
				codexSubscriptionURL+"?account_id="+url.QueryEscape(accountID), accountID, nil)
		}()
	}
	wg.Wait()
	return helps.CodexQuotaObservation(src, time.Now())
}

// codexQuotaAccountID resolves the ChatGPT account id the way the management
// panel did: a stored id first, then the id_token claims.
func codexQuotaAccountID(auth *cliproxyauth.Auth) string {
	for _, key := range []string{"account_id", "chatgpt_account_id"} {
		stored, _ := auth.Metadata[key].(string)
		for _, id := range []string{stored, auth.Attributes[key]} {
			if id = strings.TrimSpace(id); id != "" {
				return id
			}
		}
	}
	metadataToken, _ := auth.Metadata["id_token"].(string)
	for _, raw := range []string{metadataToken, auth.Attributes["id_token"]} {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if claims, errParse := codexauth.ParseJWTToken(raw); errParse == nil && strings.TrimSpace(claims.GetAccountID()) != "" {
			return strings.TrimSpace(claims.GetAccountID())
		}
	}
	return ""
}

// codexQuotaCredential reads the plan and the subscription end stored with the
// credential, from its metadata and attributes and their id_token claims.
func codexQuotaCredential(auth *cliproxyauth.Auth) helps.CodexCredentialQuota {
	metadataToken, _ := auth.Metadata["id_token"].(string)
	return helps.CodexCredentialQuotaFrom(
		codexQuotaJSON(auth.Metadata),
		codexQuotaIDTokenClaims(metadataToken),
		codexQuotaJSON(auth.Attributes),
		codexQuotaIDTokenClaims(auth.Attributes["id_token"]),
	)
}

func codexQuotaIDTokenClaims(raw string) ojson.Result {
	if strings.TrimSpace(raw) == "" {
		return ojson.Result{}
	}
	claims, errParse := codexauth.ParseJWTToken(raw)
	if errParse != nil {
		return ojson.Result{}
	}
	return codexQuotaJSON(claims.CodexAuthInfo)
}

func codexQuotaJSON(value any) ojson.Result {
	raw, errMarshal := json.Marshal(value)
	if errMarshal != nil {
		return ojson.Result{}
	}
	return ojson.ParseBytes(raw)
}

// fetchCodexQuotaJSON sends the User-Agent and Originator that /responses
// sends: the codex-header-defaults User-Agent, then auth header rules, then
// cloaking. The endpoint headers in extra are applied last.
func (e *CodexExecutor) fetchCodexQuotaJSON(ctx context.Context, auth *cliproxyauth.Auth, rawURL, accountID string, extra map[string]string) ([]byte, error) {
	// The usage endpoints take the outer OAuth token. codexCredsForRequest, in
	// PrepareRequest, picks the SIWC token for a non-Codex User-Agent, and
	// codex-header-defaults can set one, so the poll is marked as a Codex
	// client request.
	ctx = cliproxyauth.WithCodexClientRequest(ctx, true)
	req, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if errRequest != nil {
		return nil, fmt.Errorf("create codex quota request: %w", errRequest)
	}
	req.Header.Set("Accept", "application/json")
	cfgUserAgent, _ := codexHeaderDefaults(e.cfg, auth)
	ensureHeaderWithConfigPrecedence(req.Header, nil, "User-Agent", cfgUserAgent, codexUserAgent)
	req.Header.Set("Originator", codexOriginator)
	if accountID != "" {
		req.Header.Set("Chatgpt-Account-Id", accountID)
	}
	if errPrepare := e.PrepareRequest(req, auth); errPrepare != nil {
		return nil, fmt.Errorf("prepare %s: %w", req.URL.Path, errPrepare)
	}
	applyCodexCloakingHeaders(req.Header, e.cfg, auth)
	for name, value := range extra {
		req.Header.Set(name, value)
	}
	resp, errDo := helps.NewUtlsHTTPClient(ctx, e.cfg, auth, 0).Do(req)
	if errDo != nil {
		return nil, fmt.Errorf("fetch %s: %w", req.URL.Path, errDo)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("failed to close codex quota response body: %v", errClose)
		}
	}()
	body, errRead := io.ReadAll(resp.Body)
	if errRead != nil {
		return nil, fmt.Errorf("read %s response: %w", req.URL.Path, errRead)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("fetch %s failed with status %d", req.URL.Path, resp.StatusCode)
	}
	return body, nil
}

// PollQuota delegates quota polling to the HTTP executor.
func (e *CodexAutoExecutor) PollQuota(ctx context.Context, auth *cliproxyauth.Auth, trigger cliproxyauth.PollTrigger) (cliproxyauth.QuotaObservation, error) {
	if e == nil || e.httpExec == nil {
		return cliproxyauth.QuotaObservation{}, cliproxyauth.ErrQuotaNotPollable
	}
	return e.httpExec.PollQuota(ctx, auth, trigger)
}
