package auth

import (
	"net/http"
	"time"
)

var claudeHeaderWindows = []struct {
	id     WindowID
	header string
	period time.Duration
	scoped bool
}{
	{id: "five_hour", header: "5h", period: 5 * time.Hour},
	{id: "seven_day", header: "7d", period: 7 * 24 * time.Hour},
	{id: "seven_day_fable", header: "7d_oi", period: 7 * 24 * time.Hour, scoped: true},
}

// claudeHeadersObservation reads the anthropic-ratelimit-unified-* headers of
// one response. Utilization is a 0-1 fraction and the reset is unix seconds.
// The 7d_oi window is the scoped weekly limit of the model that produced the
// response, so it is dropped when the model has no scope.
func claudeHeadersObservation(h http.Header, model string, now time.Time) (QuotaObservation, bool) {
	return claudeHeadersObservationScoped(h, claudeScopeOf(model), now)
}

// claudeHeadersObservationScoped is claudeHeadersObservation with the scope of
// the 7d_oi window already decided. The usage poll has no model, and its 7d_oi
// block describes the Fable family, so the poll adapter passes that scope.
func claudeHeadersObservationScoped(h http.Header, scope string, now time.Time) (QuotaObservation, bool) {
	obs := QuotaObservation{Provider: ProviderClaude, Source: SourceHeaders, ObservedAt: now}
	for _, spec := range claudeHeaderWindows {
		if spec.scoped && scope == "" {
			continue
		}
		prefix := "Anthropic-Ratelimit-Unified-" + spec.header + "-"
		rawUtilization := quotaHeaderValue(h, prefix+"Utilization")
		rawReset := quotaHeaderValue(h, prefix+"Reset")
		if rawUtilization == "" && rawReset == "" {
			continue
		}
		window := QuotaWindowObs{
			ID:         spec.id,
			Period:     spec.period,
			ResetAt:    parseQuotaResetAt(rawReset, "", now),
			Section:    SectionHeaders,
			Source:     SourceHeaders,
			ObservedAt: now,
		}
		if spec.scoped {
			window.ModelScope = scope
		}
		if utilization, ok := parseQuotaFloat(rawUtilization); ok {
			window.Used = KnownPercent(utilization * 100)
		}
		obs.Windows = append(obs.Windows, window)
	}
	if len(obs.Windows) == 0 {
		return QuotaObservation{}, false
	}
	return obs, true
}
