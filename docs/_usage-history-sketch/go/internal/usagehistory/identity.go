package usagehistory

// Moved verbatim from internal/quotaforecast/usage.go. usagehistory owns the
// usage dimensions now, and quotaforecast imports usagehistory (for Hourly), so
// leaving them in quotaforecast would make an import cycle.
//
// Callers updated: internal/requestlog/{usage.go,service.go,store.go comment,
// service_test.go}, internal/quotaforecast/service.go (maskedKeys).
// The panel's clientKeyHash (forecastModel.ts) must stay byte-identical.

// HashClientKey returns the identifier under which a client API key's usage is
// stored: the first 12 hex characters of its SHA-256. Empty keys map to "-".
func HashClientKey(key string) string { panic("not implemented") }

// AgentFamily reduces a user agent to its first product token, e.g.
// "claude-cli/2.1.3 (external, cli)" becomes "claude-cli". Empty -> "-".
func AgentFamily(userAgent string) string { panic("not implemented") }
