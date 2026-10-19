package auth

import (
	"net/http"
	"sort"
	"strings"
	"time"
)

// maxCodexHeaderNamespaces bounds how many additional limits one response can add.
const maxCodexHeaderNamespaces = 16

// codexHeaderNamespace is one rate-limit object of a Codex response: the main
// limit (""), "code-review", or one additional limit by its header name.
type codexHeaderNamespace struct {
	name         string
	limitName    string
	allowed      *bool
	limitReached *bool
	windows      map[string]map[string]string
}

var codexWindowSuffixes = []string{"used-percent", "window-minutes", "reset-at", "reset-after-seconds"}

// codexHeadersObservation reads the x-codex-* headers of one HTTP response or
// one websocket quota event. HTTP responses namespace an additional limit by a
// short name (x-codex-bengalfox-primary-*) and websocket events by
// "additional-<limit_name>"; both carry the limit name in the -Limit-Name
// header, so one window id "additional.<limit_name>.<slot>" covers both.
func codexHeadersObservation(h http.Header, _ string, now time.Time) (QuotaObservation, bool) {
	namespaces := make(map[string]*codexHeaderNamespace)
	namespace := func(name string) *codexHeaderNamespace {
		ns, ok := namespaces[name]
		if !ok {
			ns = &codexHeaderNamespace{name: name, windows: make(map[string]map[string]string)}
			namespaces[name] = ns
		}
		return ns
	}
	for key := range h {
		lower := strings.ToLower(strings.TrimSpace(key))
		if !strings.HasPrefix(lower, "x-codex-") {
			continue
		}
		value := quotaHeaderValue(h, key)
		if value == "" {
			continue
		}
		rest := strings.TrimPrefix(lower, "x-codex-")
		if slot, suffix, ok := splitCodexWindowHeader(rest); ok {
			ns, slotName := codexNamespaceAndSlot(slot)
			target := namespace(ns)
			if target.windows[slotName] == nil {
				target.windows[slotName] = make(map[string]string)
			}
			target.windows[slotName][suffix] = value
			continue
		}
		switch {
		case strings.HasSuffix(rest, "-limit-name"):
			namespace(strings.TrimSuffix(rest, "-limit-name")).limitName = value
		case rest == "allowed":
			namespace("").allowed = parseQuotaBool(value)
		case rest == "limit-reached":
			namespace("").limitReached = parseQuotaBool(value)
		case strings.HasSuffix(rest, "-allowed"):
			namespace(strings.TrimSuffix(rest, "-allowed")).allowed = parseQuotaBool(value)
		case strings.HasSuffix(rest, "-limit-reached"):
			namespace(strings.TrimSuffix(rest, "-limit-reached")).limitReached = parseQuotaBool(value)
		}
	}

	obs := QuotaObservation{Provider: ProviderCodex, Source: SourceHeaders, ObservedAt: now}
	names := make([]string, 0, len(namespaces))
	for name := range namespaces {
		names = append(names, name)
	}
	sort.Strings(names)
	additional := 0
	for _, name := range names {
		ns := namespaces[name]
		if len(ns.windows) == 0 {
			continue
		}
		if name != "" && name != "code-review" {
			if additional >= maxCodexHeaderNamespaces {
				continue
			}
			additional++
		}
		for _, slot := range []string{"primary", "secondary"} {
			fields := ns.windows[slot]
			if fields == nil {
				continue
			}
			minutes, hasMinutes := parseQuotaFloat(fields["window-minutes"])
			resetAt := parseQuotaResetAt(fields["reset-at"], fields["reset-after-seconds"], now)
			used, hasUsed := parseQuotaFloat(fields["used-percent"])
			if !hasUsed && resetAt.IsZero() {
				continue
			}
			window := QuotaWindowObs{
				ID:           codexWindowID(ns, slot),
				Label:        ns.limitName,
				LimitReached: ns.limitReached != nil && *ns.limitReached,
				ResetAt:      resetAt,
				Section:      SectionHeaders,
				Source:       SourceHeaders,
				ObservedAt:   now,
			}
			if hasMinutes && minutes > 0 {
				window.Period = time.Duration(minutes) * time.Minute
			}
			if hasUsed {
				window.Used = KnownPercent(used)
			}
			obs.Windows = append(obs.Windows, window)
		}
	}

	extras := CodexExtras{}
	hasExtras := false
	if plan := quotaHeaderValue(h, "X-Codex-Plan-Type"); plan != "" {
		extras.Plan = OKSection(strings.ToLower(plan), now)
		hasExtras = true
	}
	if main := namespaces[""]; main != nil && (main.allowed != nil || main.limitReached != nil) {
		extras.Access = OKSection(CodexAccess{Allowed: main.allowed, LimitReached: main.limitReached}, now)
		hasExtras = true
	}
	credits := CodexCredits{
		HasCredits: parseQuotaBool(quotaHeaderValue(h, "X-Codex-Credits-Has-Credits")),
		Unlimited:  parseQuotaBool(quotaHeaderValue(h, "X-Codex-Credits-Unlimited")),
	}
	if balance := quotaHeaderValue(h, "X-Codex-Credits-Balance"); balance != "" {
		credits.Balance = &balance
	}
	if credits.HasCredits != nil || credits.Unlimited != nil || credits.Balance != nil {
		extras.Credits = OKSection(credits, now)
		hasExtras = true
	}
	if hasExtras {
		obs.Extras = extras
	}
	if len(obs.Windows) == 0 && !hasExtras {
		return QuotaObservation{}, false
	}
	return obs, true
}

// splitCodexWindowHeader splits "bengalfox-primary-used-percent" into the slot
// path "bengalfox-primary" and the field "used-percent".
func splitCodexWindowHeader(rest string) (slot, suffix string, ok bool) {
	for _, candidate := range codexWindowSuffixes {
		if strings.HasSuffix(rest, "-"+candidate) {
			slot = strings.TrimSuffix(rest, "-"+candidate)
			if strings.HasSuffix(slot, "-primary") || strings.HasSuffix(slot, "-secondary") || slot == "primary" || slot == "secondary" {
				return slot, candidate, true
			}
		}
	}
	return "", "", false
}

func codexNamespaceAndSlot(slot string) (string, string) {
	for _, name := range []string{"primary", "secondary"} {
		if slot == name {
			return "", name
		}
		if strings.HasSuffix(slot, "-"+name) {
			return strings.TrimSuffix(slot, "-"+name), name
		}
	}
	return "", slot
}

func codexWindowID(ns *codexHeaderNamespace, slot string) WindowID {
	switch ns.name {
	case "":
		return WindowID(slot)
	case "code-review":
		return WindowID("code_review." + slot)
	}
	limitName := ns.limitName
	if limitName == "" {
		limitName = strings.TrimPrefix(ns.name, "additional-")
	}
	return WindowID("additional." + limitName + "." + slot)
}

func parseQuotaBool(value string) *bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true":
		v := true
		return &v
	case "false":
		v := false
		return &v
	default:
		return nil
	}
}
