package usagehistory

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// HashClientKey returns the identifier under which a client API key's usage is
// stored: the first 12 hex characters of its SHA-256. Empty keys map to "-".
// The panel's clientKeyHash must stay byte-identical.
func HashClientKey(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return "-"
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])[:12]
}

// AgentFamily reduces a user agent to its first product token, e.g.
// "claude-cli/2.1.3 (external, cli)" becomes "claude-cli". Empty -> "-".
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
