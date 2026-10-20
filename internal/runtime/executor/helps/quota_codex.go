package helps

import (
	"errors"
	"strings"
	"time"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
)

// CodexQuotaSources holds the results of the three Codex poll requests. A nil
// body with a nil error means the request was not made, and its section stays
// absent.
type CodexQuotaSources struct {
	Usage           []byte
	UsageErr        error
	Subscription    []byte
	SubscriptionErr error
	ResetCredits    []byte
	ResetCreditsErr error
}

// CodexQuotaObservation builds the poll observation of a ChatGPT-login Codex
// credential. The usage payload owns the windows, the plan, the access flags
// and the credits; the subscription and reset-credit payloads own their
// sections. Each section fails on its own. The error is non-nil only when no
// section succeeded.
func CodexQuotaObservation(src CodexQuotaSources, now time.Time) (cliproxyauth.QuotaObservation, error) {
	obs := cliproxyauth.QuotaObservation{
		Provider:   cliproxyauth.ProviderCodex,
		Source:     cliproxyauth.SourcePoll,
		ObservedAt: now,
		Sections:   map[cliproxyauth.SectionID]cliproxyauth.SectionStatus{},
	}
	extras := cliproxyauth.CodexExtras{}
	if src.UsageErr != nil {
		obs.Sections[cliproxyauth.SectionCodexUsage] = cliproxyauth.SectionFailed
		msg := src.UsageErr.Error()
		extras.Plan = cliproxyauth.FailedSection[string](msg, now)
		extras.Access = cliproxyauth.FailedSection[cliproxyauth.CodexAccess](msg, now)
		extras.Credits = cliproxyauth.FailedSection[cliproxyauth.CodexCredits](msg, now)
	} else {
		obs.Sections[cliproxyauth.SectionCodexUsage] = cliproxyauth.SectionOK
		root := gjson.ParseBytes(src.Usage)
		obs.Windows = codexUsageQuotaWindows(root, now)
		rateLimit := firstCodexQuotaResult(root, "rate_limit", "rateLimit")
		extras.Plan = cliproxyauth.OKSection(strings.ToLower(strings.TrimSpace(firstCodexQuotaResultString(root, "plan_type", "planType"))), now)
		extras.Access = cliproxyauth.OKSection(cliproxyauth.CodexAccess{
			Allowed:      codexQuotaBool(rateLimit.Get("allowed")),
			LimitReached: codexQuotaBool(firstCodexQuotaResult(rateLimit, "limit_reached", "limitReached")),
		}, now)
		extras.Credits = cliproxyauth.OKSection(codexUsageCredits(root.Get("credits")), now)
	}
	switch {
	case src.SubscriptionErr != nil:
		extras.Subscription = cliproxyauth.FailedSection[cliproxyauth.CodexSubscription](src.SubscriptionErr.Error(), now)
	case src.Subscription != nil:
		activeUntil, _ := parseQuotaInstant(firstCodexQuotaResult(gjson.ParseBytes(src.Subscription), "active_until", "activeUntil"))
		extras.Subscription = cliproxyauth.OKSection(cliproxyauth.CodexSubscription{ActiveUntil: cliproxyauth.Instant{Time: activeUntil}}, now)
	}
	switch {
	case src.ResetCreditsErr != nil:
		extras.ResetCredits = cliproxyauth.FailedSection[cliproxyauth.CodexResetCredits](src.ResetCreditsErr.Error(), now)
	case src.ResetCredits != nil:
		credits, ok := codexResetCredits(gjson.ParseBytes(src.ResetCredits))
		if ok {
			extras.ResetCredits = cliproxyauth.OKSection(credits, now)
		} else {
			extras.ResetCredits = cliproxyauth.FailedSection[cliproxyauth.CodexResetCredits]("reset credits payload has no credits, available_count, or applicable_available_count", now)
		}
	}
	obs.Extras = extras
	if src.UsageErr != nil && extras.Subscription.Status != cliproxyauth.SectionOK && extras.ResetCredits.Status != cliproxyauth.SectionOK {
		return obs, errors.Join(src.UsageErr, src.SubscriptionErr, src.ResetCreditsErr)
	}
	return obs, nil
}

// codexUsageQuotaWindows reads the main, code-review, and additional rate
// limits of /wham/usage. Window ids match the passive header parser, so a poll
// and a response describe the same window.
func codexUsageQuotaWindows(root gjson.Result, now time.Time) []cliproxyauth.QuotaWindowObs {
	var windows []cliproxyauth.QuotaWindowObs
	windows = codexRateLimitWindows(windows, "", "", firstCodexQuotaResult(root, "rate_limit", "rateLimit"), now)
	windows = codexRateLimitWindows(windows, "code_review.", "", firstCodexQuotaResult(root, "code_review_rate_limit", "codeReviewRateLimit"), now)
	additional := firstCodexQuotaResult(root, "additional_rate_limits", "additionalRateLimits")
	if additional.IsArray() {
		for _, item := range additional.Array() {
			name := strings.TrimSpace(firstCodexQuotaResultString(item, "limit_name", "limitName", "metered_feature", "meteredFeature"))
			if name == "" {
				continue
			}
			windows = codexRateLimitWindows(windows, "additional."+name+".", name, firstCodexQuotaResult(item, "rate_limit", "rateLimit"), now)
		}
	}
	return windows
}

func codexRateLimitWindows(windows []cliproxyauth.QuotaWindowObs, idPrefix, label string, rateLimit gjson.Result, now time.Time) []cliproxyauth.QuotaWindowObs {
	if !rateLimit.IsObject() {
		return windows
	}
	limitReached := firstCodexQuotaResult(rateLimit, "limit_reached", "limitReached").Bool()
	for _, slot := range []struct{ name, key, altKey string }{
		{"primary", "primary_window", "primaryWindow"},
		{"secondary", "secondary_window", "secondaryWindow"},
	} {
		node := firstCodexQuotaResult(rateLimit, slot.key, slot.altKey)
		if !node.IsObject() {
			continue
		}
		window := cliproxyauth.QuotaWindowObs{
			ID:           cliproxyauth.WindowID(idPrefix + slot.name),
			Label:        label,
			LimitReached: limitReached,
			Section:      cliproxyauth.SectionCodexUsage,
			Source:       cliproxyauth.SourcePoll,
			ObservedAt:   now,
		}
		if used, ok := quotaNumber(firstCodexQuotaResult(node, "used_percent", "usedPercent")); ok {
			window.Used = cliproxyauth.KnownPercent(used)
		}
		if seconds, ok := quotaNumber(firstCodexQuotaResult(node, "limit_window_seconds", "limitWindowSeconds")); ok && seconds > 0 {
			window.Period = time.Duration(seconds) * time.Second
		}
		if resetAt, ok := parseQuotaInstant(firstCodexQuotaResult(node, "reset_at", "resetAt")); ok {
			window.ResetAt = resetAt
		} else if after, ok := quotaNumber(firstCodexQuotaResult(node, "reset_after_seconds", "resetAfterSeconds")); ok && after >= 0 {
			window.ResetAt = now.Add(time.Duration(after * float64(time.Second)))
		}
		if _, known := window.Used.Get(); !known && window.ResetAt.IsZero() && !limitReached {
			continue
		}
		windows = append(windows, window)
	}
	return windows
}

func codexUsageCredits(node gjson.Result) cliproxyauth.CodexCredits {
	credits := cliproxyauth.CodexCredits{
		HasCredits: codexQuotaBool(firstCodexQuotaResult(node, "has_credits", "hasCredits")),
		Unlimited:  codexQuotaBool(node.Get("unlimited")),
	}
	if balance := codexQuotaScalarValue(node.Get("balance")); balance != "" {
		credits.Balance = &balance
	}
	return credits
}

// codexResetCredits reads /wham/rate-limit-reset-credits. Only credits of
// reset_type codex_rate_limits count, as in the management panel.
func codexResetCredits(root gjson.Result) (cliproxyauth.CodexResetCredits, bool) {
	if !root.IsObject() {
		return cliproxyauth.CodexResetCredits{}, false
	}
	hasShape := false
	for _, key := range []string{"credits", "available_count", "availableCount", "applicable_available_count", "applicableAvailableCount"} {
		if root.Get(key).Exists() {
			hasShape = true
			break
		}
	}
	if !hasShape {
		return cliproxyauth.CodexResetCredits{}, false
	}
	out := cliproxyauth.CodexResetCredits{
		Available:           codexQuotaInt(firstCodexQuotaResult(root, "available_count", "availableCount")),
		ApplicableAvailable: codexQuotaInt(firstCodexQuotaResult(root, "applicable_available_count", "applicableAvailableCount")),
	}
	for _, item := range root.Get("credits").Array() {
		if !strings.EqualFold(strings.TrimSpace(firstCodexQuotaResultString(item, "reset_type", "resetType")), "codex_rate_limits") {
			continue
		}
		granted, _ := parseQuotaInstant(firstCodexQuotaResult(item, "granted_at", "grantedAt"))
		expires, _ := parseQuotaInstant(firstCodexQuotaResult(item, "expires_at", "expiresAt"))
		out.Credits = append(out.Credits, cliproxyauth.CodexResetCredit{
			ID:        strings.TrimSpace(item.Get("id").String()),
			Status:    strings.TrimSpace(item.Get("status").String()),
			GrantedAt: cliproxyauth.Instant{Time: granted},
			ExpiresAt: cliproxyauth.Instant{Time: expires},
		})
	}
	return out, true
}

func codexQuotaBool(value gjson.Result) *bool {
	switch value.Type {
	case gjson.True, gjson.False:
		v := value.Bool()
		return &v
	default:
		return nil
	}
}

func codexQuotaInt(value gjson.Result) *int {
	number, ok := quotaNumber(value)
	if !ok {
		return nil
	}
	v := int(number)
	return &v
}
