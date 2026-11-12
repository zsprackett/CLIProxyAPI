package auth

import (
	"maps"
	"net/http"
	"strings"
)

const maxQuotaSignalValue = 512

// quotaHeaderValue returns the last value of a header, or "" when it is empty,
// oversized, or carries a control character. Observed values reach the
// plain-text upstream request log, so a value containing CR/LF could otherwise
// forge a header line there.
func quotaHeaderValue(h http.Header, name string) string {
	values := h[http.CanonicalHeaderKey(strings.TrimSpace(name))]
	if len(values) == 0 {
		return ""
	}
	value := strings.TrimSpace(values[len(values)-1])
	if value == "" || len(value) > maxQuotaSignalValue {
		return ""
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return ""
		}
	}
	return value
}

// cooldownFieldsOf copies only scheduler cooldown fields, without the private
// cooldown signals, for persistence.
func cooldownFieldsOf(q QuotaState) QuotaState {
	return QuotaState{
		Exceeded:      q.Exceeded,
		Reason:        q.Reason,
		NextRecoverAt: q.NextRecoverAt,
		BackoffLevel:  q.BackoffLevel,
	}
}

// applyCooldownFields writes a cooldown transition, including the rejected
// quota headers captured with it.
func applyCooldownFields(dst *QuotaState, cooldown QuotaState) {
	if dst == nil {
		return
	}
	dst.Exceeded = cooldown.Exceeded
	dst.Reason = cooldown.Reason
	dst.NextRecoverAt = cooldown.NextRecoverAt
	dst.BackoffLevel = cooldown.BackoffLevel
	dst.cooldownSignals = maps.Clone(cooldown.cooldownSignals)
	dst.cooldownEvaluated = cooldown.Exceeded || cooldown.cooldownEvaluated
}

// rejectedClaudeSignalsForCooldown returns the anthropic-ratelimit-unified-*
// headers of a Claude response when at least one of them reports "rejected".
// A local cooldown 429 replays them to the client.
func rejectedClaudeSignalsForCooldown(provider string, headers http.Header) map[string]string {
	if !strings.EqualFold(strings.TrimSpace(provider), "claude") || len(headers) == 0 {
		return nil
	}
	signals := make(map[string]string)
	for key := range headers {
		name := http.CanonicalHeaderKey(strings.TrimSpace(key))
		if !strings.HasPrefix(strings.ToLower(name), "anthropic-ratelimit-unified-") {
			continue
		}
		if value := quotaHeaderValue(headers, name); value != "" {
			signals[name] = value
		}
	}
	if !hasRejectedClaudeUnifiedSignals(signals) {
		return nil
	}
	return signals
}

// sanitizePersistedClaudeCooldownSignals re-validates cooldown signals read
// from disk.
func sanitizePersistedClaudeCooldownSignals(raw map[string]string) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	headers := make(http.Header, len(raw))
	for key, val := range raw {
		headers.Set(key, val)
	}
	return rejectedClaudeSignalsForCooldown("claude", headers)
}
