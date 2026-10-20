package helps

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

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
