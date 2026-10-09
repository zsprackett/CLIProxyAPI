package helps

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// claudeUsageQuotaWindows maps /api/oauth/usage window keys to the
// anthropic-ratelimit-unified-<window>-* header names. iguana_necktie is the
// Fable-scoped weekly window that responses report as 7d_oi.
var claudeUsageQuotaWindows = []struct {
	key    string
	header string
}{
	{key: "five_hour", header: "5h"},
	{key: "seven_day", header: "7d"},
	{key: "iguana_necktie", header: "7d_oi"},
}

// ClaudeUsageQuotaHeaders converts a Claude /api/oauth/usage payload into the
// anthropic-ratelimit-unified-* headers live responses carry, so a polled
// snapshot reads exactly like an observed one. The payload reports utilization
// as a 0-100 percentage and resets_at as RFC 3339; the headers use a 0-1
// fraction and unix seconds.
func ClaudeUsageQuotaHeaders(body []byte) http.Header {
	root := gjson.ParseBytes(body)
	headers := make(http.Header)
	for _, window := range claudeUsageQuotaWindows {
		node := root.Get(window.key)
		if !node.IsObject() {
			continue
		}
		prefix := "Anthropic-Ratelimit-Unified-" + window.header + "-"
		if utilization := node.Get("utilization"); utilization.Type == gjson.Number {
			headers.Set(prefix+"Utilization", strconv.FormatFloat(utilization.Float()/100, 'f', -1, 64))
		}
		if resetAt, ok := parseQuotaInstant(node.Get("resets_at")); ok {
			headers.Set(prefix+"Reset", strconv.FormatInt(resetAt.Unix(), 10))
		}
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

// CodexUsageQuotaHeaders converts a Codex /backend-api/wham/usage payload into
// the x-codex-* headers live responses carry. The payload names windows
// primary_window/secondary_window and sizes them in seconds; the headers use
// primary/secondary and minutes.
func CodexUsageQuotaHeaders(body []byte) http.Header {
	root := gjson.ParseBytes(body)
	rateLimit := firstCodexQuotaResult(root, "rate_limit", "rateLimit")
	if !rateLimit.IsObject() {
		return nil
	}
	headers := make(http.Header)
	hasWindow := addCodexUsageWindowHeaders(headers, "X-Codex-Primary-", firstCodexQuotaResult(rateLimit, "primary_window", "primaryWindow"))
	if addCodexUsageWindowHeaders(headers, "X-Codex-Secondary-", firstCodexQuotaResult(rateLimit, "secondary_window", "secondaryWindow")) {
		hasWindow = true
	}
	if !hasWindow {
		return nil
	}
	setCodexQuotaScalarHeaderFromResult(headers, "X-Codex-Allowed", rateLimit, "allowed")
	setCodexQuotaScalarHeaderFromResult(headers, "X-Codex-Limit-Reached", rateLimit, "limit_reached", "limitReached")
	if credits := firstCodexQuotaResult(root, "credits"); credits.IsObject() {
		setCodexQuotaScalarHeaderFromResult(headers, "X-Codex-Credits-Has-Credits", credits, "has_credits", "hasCredits")
		setCodexQuotaScalarHeaderFromResult(headers, "X-Codex-Credits-Unlimited", credits, "unlimited")
		setCodexQuotaScalarHeaderFromResult(headers, "X-Codex-Credits-Balance", credits, "balance")
	}
	if planType := firstCodexQuotaResultString(root, "plan_type", "planType"); validCodexQuotaEventText(planType) {
		headers.Set("X-Codex-Plan-Type", planType)
	}
	return headers
}

func addCodexUsageWindowHeaders(headers http.Header, prefix string, window gjson.Result) bool {
	if !window.IsObject() {
		return false
	}
	used := codexQuotaScalarValue(firstCodexQuotaResult(window, "used_percent", "usedPercent"))
	if used == "" {
		return false
	}
	headers.Set(prefix+"Used-Percent", used)
	if seconds := firstCodexQuotaResult(window, "limit_window_seconds", "limitWindowSeconds"); seconds.Int() > 0 {
		headers.Set(prefix+"Window-Minutes", strconv.FormatInt(seconds.Int()/60, 10))
	}
	if resetAt := firstCodexQuotaResult(window, "reset_at", "resetAt"); resetAt.Int() > 0 {
		headers.Set(prefix+"Reset-At", strconv.FormatInt(resetAt.Int(), 10))
	}
	if resetAfter := firstCodexQuotaResult(window, "reset_after_seconds", "resetAfterSeconds"); resetAfter.Exists() && resetAfter.Int() >= 0 {
		headers.Set(prefix+"Reset-After-Seconds", strconv.FormatInt(resetAfter.Int(), 10))
	}
	return true
}

// xaiBillingSummary is the active billing period of one Grok CLI billing payload.
type xaiBillingSummary struct {
	periodType  string
	usedPercent *float64
	start       time.Time
	end         time.Time
}

// XAIBillingQuotaHeaders summarizes the Grok CLI billing payloads into
// x-xai-billing-* headers. The weekly (format=credits) payload wins when it
// reports a period; the monthly payload is the fallback. This mirrors the
// management UI's billing summary so a polled snapshot matches a live fetch.
func XAIBillingQuotaHeaders(weeklyBody, monthlyBody []byte, now time.Time) http.Header {
	summary, ok := parseXAIBillingSummary(weeklyBody, now)
	if !ok {
		summary, ok = parseXAIBillingSummary(monthlyBody, now)
	}
	if !ok {
		return nil
	}
	headers := make(http.Header)
	headers.Set("X-Xai-Billing-Period-Type", summary.periodType)
	if summary.usedPercent != nil {
		headers.Set("X-Xai-Billing-Used-Percent", strconv.FormatFloat(*summary.usedPercent, 'f', -1, 64))
	}
	if !summary.end.IsZero() {
		headers.Set("X-Xai-Billing-Reset-At", strconv.FormatInt(summary.end.Unix(), 10))
		if !summary.start.IsZero() && summary.end.After(summary.start) {
			headers.Set("X-Xai-Billing-Window-Minutes", strconv.FormatInt(int64(summary.end.Sub(summary.start)/time.Minute), 10))
		}
	}
	return headers
}

func parseXAIBillingSummary(body []byte, now time.Time) (xaiBillingSummary, bool) {
	config := gjson.GetBytes(body, "config")
	if !config.IsObject() {
		return xaiBillingSummary{}, false
	}
	period := firstCodexQuotaResult(config, "currentPeriod", "current_period")
	rawPeriodType := strings.ToLower(period.Get("type").String())
	rawCreditUsage := firstCodexQuotaResult(config, "creditUsagePercent", "credit_usage_percent")
	creditUsage, hasCreditUsage := quotaNumber(rawCreditUsage)
	productUsage := firstCodexQuotaResult(config, "productUsage", "product_usage")
	monthlyLimit, hasMonthlyLimit := xaiBillingCents(firstCodexQuotaResult(config, "monthlyLimit", "monthly_limit"))
	used, hasUsed := xaiBillingCents(config.Get("used"))
	_, hasOnDemandCap := xaiBillingCents(firstCodexQuotaResult(config, "onDemandCap", "on_demand_cap"))
	billingStart := firstCodexQuotaResult(config, "billingPeriodStart", "billing_period_start")
	billingEnd := firstCodexQuotaResult(config, "billingPeriodEnd", "billing_period_end")

	hasWeekly := hasCreditUsage || strings.Contains(rawPeriodType, "weekly") ||
		(productUsage.IsArray() && len(productUsage.Array()) > 0)
	hasMonthly := hasMonthlyLimit || hasUsed ||
		(!hasWeekly && (hasOnDemandCap || strings.TrimSpace(billingEnd.String()) != ""))
	if !hasWeekly && !hasMonthly {
		return xaiBillingSummary{}, false
	}

	if hasWeekly {
		summary := xaiBillingSummary{periodType: "weekly"}
		if strings.Contains(rawPeriodType, "monthly") && !strings.Contains(rawPeriodType, "weekly") {
			summary.periodType = "monthly"
		}
		periodStart, hasPeriodStart := parseQuotaInstant(period.Get("start"))
		periodEnd, hasPeriodEnd := parseQuotaInstant(period.Get("end"))
		if !rawCreditUsage.Exists() && hasPeriodStart && hasPeriodEnd && !now.Before(periodStart) && now.Before(periodEnd) {
			// creditUsagePercent is an implicit-presence proto3 float, so the
			// response omits it at zero. Grok's own clients read an omitted value
			// within the active period as 0% used. A malformed value stays unknown.
			creditUsage, hasCreditUsage = 0, true
		}
		if hasCreditUsage {
			summary.usedPercent = &creditUsage
		}
		summary.start = firstQuotaInstant(period.Get("start"), billingStart)
		summary.end = firstQuotaInstant(period.Get("end"), billingEnd)
		return summary, true
	}

	summary := xaiBillingSummary{periodType: "monthly"}
	if hasUsed && hasMonthlyLimit && monthlyLimit > 0 {
		percent := min(used, monthlyLimit) / monthlyLimit * 100
		summary.usedPercent = &percent
	}
	summary.start = firstQuotaInstant(billingStart)
	summary.end = firstQuotaInstant(billingEnd)
	return summary, true
}

// xaiBillingCents reads a cent amount that is either a number, a numeric
// string, or an object wrapping the amount in "val".
func xaiBillingCents(value gjson.Result) (float64, bool) {
	if value.IsObject() {
		value = value.Get("val")
	}
	return quotaNumber(value)
}

func quotaNumber(value gjson.Result) (float64, bool) {
	switch value.Type {
	case gjson.Number:
		return value.Float(), true
	case gjson.String:
		parsed, errParse := strconv.ParseFloat(strings.TrimSpace(value.String()), 64)
		return parsed, errParse == nil
	default:
		return 0, false
	}
}

func firstQuotaInstant(values ...gjson.Result) time.Time {
	for _, value := range values {
		if instant, ok := parseQuotaInstant(value); ok {
			return instant
		}
	}
	return time.Time{}
}

// parseQuotaInstant reads an RFC 3339 timestamp or a unix timestamp in seconds
// or milliseconds.
func parseQuotaInstant(value gjson.Result) (time.Time, bool) {
	if number, ok := quotaNumber(value); ok && number > 0 {
		if number > 1e12 {
			return time.UnixMilli(int64(number)), true
		}
		return time.Unix(int64(number), 0), true
	}
	if value.Type != gjson.String {
		return time.Time{}, false
	}
	parsed, errParse := time.Parse(time.RFC3339Nano, strings.TrimSpace(value.String()))
	if errParse != nil {
		return time.Time{}, false
	}
	return parsed, true
}
