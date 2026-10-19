package auth

import (
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
