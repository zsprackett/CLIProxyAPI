package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	codexauth "github.com/router-for-me/CLIProxyAPI/v8/internal/auth/codex"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
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
	if token, _ := codexCreds(auth); strings.TrimSpace(token) == "" {
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

func codexQuotaIDTokenClaims(raw string) gjson.Result {
	if strings.TrimSpace(raw) == "" {
		return gjson.Result{}
	}
	claims, errParse := codexauth.ParseJWTToken(raw)
	if errParse != nil {
		return gjson.Result{}
	}
	return codexQuotaJSON(claims.CodexAuthInfo)
}

func codexQuotaJSON(value any) gjson.Result {
	raw, errMarshal := json.Marshal(value)
	if errMarshal != nil {
		return gjson.Result{}
	}
	return gjson.ParseBytes(raw)
}

func (e *CodexExecutor) fetchCodexQuotaJSON(ctx context.Context, auth *cliproxyauth.Auth, rawURL, accountID string, extra map[string]string) ([]byte, error) {
	req, errRequest := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if errRequest != nil {
		return nil, fmt.Errorf("create codex quota request: %w", errRequest)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", codexUserAgent)
	req.Header.Set("Originator", codexOriginator)
	if accountID != "" {
		req.Header.Set("Chatgpt-Account-Id", accountID)
	}
	for name, value := range extra {
		req.Header.Set(name, value)
	}
	resp, errDo := e.HttpRequest(ctx, auth, req)
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
