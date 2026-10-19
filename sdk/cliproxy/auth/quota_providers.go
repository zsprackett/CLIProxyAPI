package auth

import (
	"net/http"
	"strings"
	"time"
)

// quotaProvider is one row of the provider registry. Adding a provider means
// adding a row here, one extras struct, one poll parser, and one UI body.
type quotaProvider struct {
	newExtras func() QuotaExtras
	// fromHeaders parses the passive quota headers of one response. nil means
	// the provider has no passive signal.
	fromHeaders func(h http.Header, model string, now time.Time) (QuotaObservation, bool)
	// classOf maps a window to its pool class, or "" for a display-only window.
	classOf    func(id WindowID, period time.Duration) WindowClass
	classOrder []WindowClass
	// showsCapacity decides a cooldown clear on the poll observation alone.
	showsCapacity func(obs QuotaObservation) bool
	// scopeOf maps a model to its window scope family. nil means no scoped windows.
	scopeOf func(model string) string
}

var quotaProviders = map[ProviderKind]quotaProvider{
	ProviderClaude: {
		newExtras:   func() QuotaExtras { return ClaudeExtras{} },
		fromHeaders: claudeHeadersObservation,
		classOf: func(id WindowID, _ time.Duration) WindowClass {
			switch id {
			case "five_hour":
				return "claude:5h"
			case "seven_day":
				return "claude:7d"
			case "seven_day_fable":
				return "claude:7d_oi"
			default:
				return ""
			}
		},
		classOrder:    []WindowClass{"claude:5h", "claude:7d", "claude:7d_oi"},
		showsCapacity: claudeShowsCapacity,
		scopeOf:       claudeScopeOf,
	},
	ProviderCodex: {
		newExtras:     func() QuotaExtras { return CodexExtras{} },
		fromHeaders:   codexHeadersObservation,
		classOf:       codexClassOf,
		classOrder:    []WindowClass{"codex:5h", "codex:weekly", "codex:monthly"},
		showsCapacity: codexShowsCapacity,
	},
	ProviderXAI: {
		newExtras: func() QuotaExtras { return XAIExtras{} },
		classOf: func(id WindowID, _ time.Duration) WindowClass {
			switch id {
			case "weekly":
				return "xai:weekly"
			case "monthly":
				return "xai:monthly"
			default:
				return ""
			}
		},
		classOrder:    []WindowClass{"xai:weekly", "xai:monthly"},
		showsCapacity: func(QuotaObservation) bool { return false },
	},
	ProviderDevin: {
		newExtras: func() QuotaExtras { return DevinExtras{} },
		classOf: func(id WindowID, _ time.Duration) WindowClass {
			switch id {
			case "daily":
				return "devin:daily"
			case "weekly":
				return "devin:weekly"
			default:
				return ""
			}
		},
		classOrder:    []WindowClass{"devin:daily", "devin:weekly"},
		showsCapacity: func(QuotaObservation) bool { return false },
	},
}

func providerKindOf(provider string) (ProviderKind, bool) {
	kind := ProviderKind(strings.ToLower(strings.TrimSpace(provider)))
	_, ok := quotaProviders[kind]
	return kind, ok
}

// ProviderSupportsQuotaObservation reports whether the provider has a row in
// the quota registry, so its credentials carry a QuotaSnapshot.
func ProviderSupportsQuotaObservation(provider string) bool {
	_, ok := providerKindOf(provider)
	return ok
}

// quotaScopeOf returns the window scope family of model under provider, or "".
func quotaScopeOf(provider ProviderKind, model string) string {
	row, ok := quotaProviders[provider]
	if !ok || row.scopeOf == nil {
		return ""
	}
	return row.scopeOf(model)
}

func claudeScopeOf(model string) string {
	if strings.Contains(strings.ToLower(strings.TrimSpace(model)), "claude-fable") {
		return "fable"
	}
	return ""
}

// claudeShowsCapacity needs both account windows with known room, because a
// poll that returns only one window cannot prove the other has recovered.
func claudeShowsCapacity(obs QuotaObservation) bool {
	seen := make(map[WindowID]bool, 2)
	for _, w := range obs.Windows {
		if w.ID != "five_hour" && w.ID != "seven_day" {
			continue
		}
		used, known := w.Used.Get()
		if !known || used >= 100 || w.LimitReached {
			return false
		}
		seen[w.ID] = true
	}
	return seen["five_hour"] && seen["seven_day"]
}

// codexClassOf keys the account windows by period, because Codex names both
// the 5-hour and the weekly limit "primary" or "secondary" depending on the plan.
func codexClassOf(id WindowID, period time.Duration) WindowClass {
	if id != "primary" && id != "secondary" {
		return ""
	}
	minutes := period / time.Minute
	switch {
	case minutes <= 0:
		return ""
	case minutes > weeklyWindowMinutes:
		return "codex:monthly"
	case minutes == weeklyWindowMinutes:
		return "codex:weekly"
	default:
		return "codex:5h"
	}
}

func codexShowsCapacity(obs QuotaObservation) bool {
	classed := 0
	for _, w := range obs.Windows {
		if codexClassOf(w.ID, w.Period) == "" {
			continue
		}
		used, known := w.Used.Get()
		if w.LimitReached || (known && used >= 100) {
			return false
		}
		if known {
			classed++
		}
	}
	if classed == 0 {
		return false
	}
	extras, ok := obs.Extras.(CodexExtras)
	if !ok || extras.Access.status() != SectionOK || extras.Access.Value == nil {
		return true
	}
	access := extras.Access.Value
	if access.LimitReached != nil && *access.LimitReached {
		return false
	}
	return access.Allowed == nil || *access.Allowed
}
