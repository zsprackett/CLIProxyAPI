package quotaforecast

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
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
		KeyHash:  HashClientKey(record.APIKey),
		Agent:    AgentFamily(internallogging.GetClientRequestMetadata(ctx).UserAgent),
		Requests: 1,
		Tokens:   uncachedTokens(record.Detail),
	})
}

// HashClientKey returns the identifier under which a client API key's usage is
// stored: the first 12 hex characters of its SHA-256. Empty keys map to "-".
func HashClientKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "-"
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:12]
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

// AgentFamily reduces a user agent to its first product token, e.g.
// "claude-cli/2.1.3 (external, cli)" becomes "claude-cli".
func AgentFamily(userAgent string) string {
	userAgent = strings.TrimSpace(userAgent)
	if userAgent == "" {
		return "-"
	}
	if idx := strings.IndexAny(userAgent, "/ ("); idx > 0 {
		userAgent = userAgent[:idx]
	}
	userAgent = strings.ToLower(userAgent)
	if len(userAgent) > 40 {
		userAgent = userAgent[:40]
	}
	return userAgent
}
