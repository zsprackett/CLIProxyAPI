package quotaforecast

import (
	"context"
	"strings"
	"time"

	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory"
	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

// forecastProviders lists the providers whose quota windows are recorded.
var forecastProviders = map[string]struct{}{"claude": {}, "codex": {}, "xai": {}, "devin": {}}

// HandleUsage implements usage.Plugin. It aggregates uncached tokens per hour,
// credential, model, client key hash and client agent for quota attribution.
func (s *Service) HandleUsage(ctx context.Context, record coreusage.Record) {
	if s == nil || !s.enabled.Load() {
		return
	}
	provider := strings.ToLower(strings.TrimSpace(record.Provider))
	if _, ok := forecastProviders[provider]; !ok || strings.TrimSpace(record.AuthID) == "" {
		return
	}
	model := strings.TrimSpace(record.Model)
	if model == "" {
		model = strings.TrimSpace(record.Alias)
	}
	if model == "" {
		model = "unknown"
	}
	requestedAt := record.RequestedAt
	if requestedAt.IsZero() {
		requestedAt = s.now()
	}
	s.hist.addUsage(UsageBucket{
		Hour:     requestedAt.Truncate(time.Hour).Unix(),
		AuthID:   record.AuthID,
		Model:    model,
		KeyHash:  usagehistory.HashClientKey(record.APIKey),
		Agent:    usagehistory.AgentFamily(internallogging.GetClientRequestMetadata(ctx).UserAgent),
		Requests: 1,
		Tokens:   uncachedTokens(record.Detail),
	})
}

// uncachedTokens approximates the tokens that count against a subscription quota.
// Cache reads are excluded because they are billed at a small fraction of fresh tokens.
func uncachedTokens(detail coreusage.Detail) int64 {
	if b := detail.TokenBreakdown; b.Valid() {
		return b.TotalTokens - b.Input.CacheReadTokens
	}
	total := detail.TotalTokens
	if total <= 0 {
		total = detail.InputTokens + detail.OutputTokens
	}
	cached := detail.CachedTokens
	if detail.CacheReadTokens > cached {
		cached = detail.CacheReadTokens
	}
	if total -= cached; total < 0 {
		return 0
	}
	return total
}
