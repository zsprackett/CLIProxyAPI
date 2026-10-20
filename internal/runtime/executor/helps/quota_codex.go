package helps

import (
	"regexp"
	"strconv"
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
	// Credential holds the plan and the subscription end stored with the
	// credential. They stand in when the upstream leaves them out, or when
	// its request fails or is skipped.
	Credential CodexCredentialQuota
}

// CodexCredentialQuota is the plan and the subscription end stored with a
// ChatGPT-login credential.
type CodexCredentialQuota struct {
	Plan        string
	ActiveUntil time.Time
}

// CodexCredentialQuotaFrom reads the plan and the subscription end from the
// stored credential sources, first match wins, as the management panel did:
// the metadata, its id_token claims, the attributes, their id_token claims.
func CodexCredentialQuotaFrom(sources ...gjson.Result) CodexCredentialQuota {
	var credential CodexCredentialQuota
	for _, source := range sources {
		if credential.Plan == "" {
			credential.Plan = strings.ToLower(strings.TrimSpace(firstCodexQuotaResultString(source, "plan_type", "planType", "chatgpt_plan_type")))
		}
		if credential.ActiveUntil.IsZero() {
			credential.ActiveUntil, _ = parseQuotaInstant(firstCodexQuotaResult(source,
				"chatgpt_subscription_active_until", "chatgptSubscriptionActiveUntil",
				"subscription_active_until", "subscriptionActiveUntil",
				"subscription.active_until", "subscription.activeUntil"))
		}
	}
	return credential
}

// CodexQuotaObservation builds the poll observation of a ChatGPT-login Codex
// credential. The usage payload owns the windows, the plan, the access flags
// and the credits; the subscription and reset-credit payloads own their
// sections. Each section fails on its own. The error is the usage error when
// that request failed: the windows are the poll, the rest rides along.
func CodexQuotaObservation(src CodexQuotaSources, now time.Time) (cliproxyauth.QuotaObservation, error) {
	obs := cliproxyauth.QuotaObservation{
		Provider:   cliproxyauth.ProviderCodex,
		Source:     cliproxyauth.SourcePoll,
		ObservedAt: now,
		Sections:   map[cliproxyauth.SectionID]cliproxyauth.SectionStatus{},
	}
	extras := cliproxyauth.CodexExtras{}
	var usageCounts cliproxyauth.CodexResetCredits
	if src.UsageErr != nil {
		obs.Sections[cliproxyauth.SectionCodexUsage] = cliproxyauth.SectionFailed
		msg := src.UsageErr.Error()
		extras.Plan = cliproxyauth.FailedSection[string](msg, now)
		if plan := src.Credential.Plan; plan != "" {
			extras.Plan.Value, extras.Plan.ValueAt = &plan, now
		}
		extras.Access = cliproxyauth.FailedSection[cliproxyauth.CodexAccess](msg, now)
		extras.Credits = cliproxyauth.FailedSection[cliproxyauth.CodexCredits](msg, now)
	} else {
		obs.Sections[cliproxyauth.SectionCodexUsage] = cliproxyauth.SectionOK
		root := gjson.ParseBytes(src.Usage)
		obs.Windows = codexUsageQuotaWindows(root, now)
		rateLimit := firstCodexQuotaResult(root, "rate_limit", "rateLimit")
		plan := strings.ToLower(strings.TrimSpace(firstCodexQuotaResultString(root, "plan_type", "planType")))
		if plan == "" {
			plan = src.Credential.Plan
		}
		extras.Plan = cliproxyauth.OKSection(plan, now)
		extras.Access = cliproxyauth.OKSection(cliproxyauth.CodexAccess{
			Allowed:      codexQuotaBool(rateLimit.Get("allowed")),
			LimitReached: codexQuotaBool(firstCodexQuotaResult(rateLimit, "limit_reached", "limitReached")),
		}, now)
		extras.Credits = cliproxyauth.OKSection(codexUsageCredits(root.Get("credits")), now)
		usageCounts = codexResetCreditCounts(firstCodexQuotaResult(root, "rate_limit_reset_credits", "rateLimitResetCredits"))
	}
	storedSubscription := cliproxyauth.CodexSubscription{ActiveUntil: cliproxyauth.Instant{Time: src.Credential.ActiveUntil}}
	switch {
	case src.SubscriptionErr != nil:
		extras.Subscription = cliproxyauth.FailedSection[cliproxyauth.CodexSubscription](src.SubscriptionErr.Error(), now)
		if !src.Credential.ActiveUntil.IsZero() {
			extras.Subscription.Value, extras.Subscription.ValueAt = &storedSubscription, now
		}
	case src.Subscription != nil:
		activeUntil, ok := parseQuotaInstant(firstCodexQuotaResult(gjson.ParseBytes(src.Subscription), "active_until", "activeUntil"))
		if !ok {
			activeUntil = src.Credential.ActiveUntil
		}
		extras.Subscription = cliproxyauth.OKSection(cliproxyauth.CodexSubscription{ActiveUntil: cliproxyauth.Instant{Time: activeUntil}}, now)
	case !src.Credential.ActiveUntil.IsZero():
		// No account id, so the subscription request was skipped.
		extras.Subscription = cliproxyauth.OKSection(storedSubscription, now)
	}
	resetCreditsFailed := func(msg string) {
		extras.ResetCredits = cliproxyauth.FailedSection[cliproxyauth.CodexResetCredits](msg, now)
		// The usage payload still says how many credits there are, so the
		// reset button keeps working while the list is unavailable.
		if counts := withCodexResetCreditCounts(cliproxyauth.CodexResetCredits{}, usageCounts); counts.Available != nil || counts.ApplicableAvailable != nil {
			extras.ResetCredits.Value, extras.ResetCredits.ValueAt = &counts, now
		}
	}
	switch {
	case src.ResetCreditsErr != nil:
		resetCreditsFailed(src.ResetCreditsErr.Error())
	case src.ResetCredits != nil:
		credits, ok := codexResetCredits(gjson.ParseBytes(src.ResetCredits))
		if ok {
			extras.ResetCredits = cliproxyauth.OKSection(withCodexResetCreditCounts(credits, usageCounts), now)
		} else {
			resetCreditsFailed("reset credits payload has no credits, available_count, or applicable_available_count")
		}
	}
	obs.Extras = extras
	return obs, src.UsageErr
}

// codexResetCreditCounts reads the two counts of a rate_limit_reset_credits
// object, as found in the details payload and in /wham/usage.
func codexResetCreditCounts(node gjson.Result) cliproxyauth.CodexResetCredits {
	return cliproxyauth.CodexResetCredits{
		Available:           codexQuotaInt(firstCodexQuotaResult(node, "available_count", "availableCount")),
		ApplicableAvailable: codexQuotaInt(firstCodexQuotaResult(node, "applicable_available_count", "applicableAvailableCount")),
	}
}

// withCodexResetCreditCounts fills the counts the way the panel did: available
// from the details, then the listed credits, then the usage payload;
// applicable from the usage payload, then the details, then available.
func withCodexResetCreditCounts(credits, usage cliproxyauth.CodexResetCredits) cliproxyauth.CodexResetCredits {
	if credits.Available == nil {
		if n := len(credits.Credits); n > 0 {
			credits.Available = &n
		} else {
			credits.Available = usage.Available
		}
	}
	switch {
	case usage.ApplicableAvailable != nil:
		credits.ApplicableAvailable = usage.ApplicableAvailable
	case credits.ApplicableAvailable == nil:
		credits.ApplicableAvailable = credits.Available
	}
	return credits
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
		for i, item := range additional.Array() {
			name := strings.TrimSpace(firstCodexQuotaResultString(item, "limit_name", "limitName", "metered_feature", "meteredFeature"))
			if name == "" {
				name = "additional-" + strconv.Itoa(i+1)
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
	if allowed := codexQuotaBool(rateLimit.Get("allowed")); allowed != nil && !*allowed {
		limitReached = true
	}
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
	if balance := codexQuotaScalarValue(node.Get("balance")); codexCreditBalancePattern.MatchString(balance) {
		credits.Balance = &balance
	}
	return credits
}

// codexCreditBalancePattern accepts the plain decimal the panel showed; any
// other text leaves the balance unknown.
var codexCreditBalancePattern = regexp.MustCompile(`^\d+(\.\d+)?$`)

// codexResetCredits reads /wham/rate-limit-reset-credits. Only an available
// codex_rate_limits credit with an expiry is listed, as in the management panel.
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
	out := codexResetCreditCounts(root)
	for _, item := range root.Get("credits").Array() {
		if !strings.EqualFold(strings.TrimSpace(firstCodexQuotaResultString(item, "reset_type", "resetType")), "codex_rate_limits") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(item.Get("status").String()), "available") {
			continue
		}
		expires, ok := parseQuotaInstant(firstCodexQuotaResult(item, "expires_at", "expiresAt"))
		if !ok {
			continue
		}
		granted, _ := parseQuotaInstant(firstCodexQuotaResult(item, "granted_at", "grantedAt"))
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
