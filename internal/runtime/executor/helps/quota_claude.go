package helps

import (
	"strings"
	"time"

	claudeauth "github.com/router-for-me/CLIProxyAPI/v8/internal/auth/claude"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
)

// claudePollFableScope is the window scope of the Fable weekly limit.
const claudePollFableScope = "fable"

// claudeUsageWindows lists the /api/oauth/usage window blocks. iguana_necktie
// is the Fable weekly limit; limits[] carries the same limit on newer accounts
// and wins when present.
var claudeUsageWindows = []struct {
	key    string
	id     cliproxyauth.WindowID
	period time.Duration
	scope  string
}{
	{key: "five_hour", id: "five_hour", period: 5 * time.Hour},
	{key: "seven_day", id: "seven_day", period: 7 * 24 * time.Hour},
	{key: "seven_day_oauth_apps", id: "seven_day_oauth_apps", period: 7 * 24 * time.Hour},
	{key: "seven_day_opus", id: "seven_day_opus", period: 7 * 24 * time.Hour},
	{key: "seven_day_sonnet", id: "seven_day_sonnet", period: 7 * 24 * time.Hour},
	{key: "seven_day_cowork", id: "seven_day_cowork", period: 7 * 24 * time.Hour},
	{key: "iguana_necktie", id: "seven_day_fable", period: 7 * 24 * time.Hour, scope: claudePollFableScope},
}

// ClaudeQuotaSources holds the results of the two Claude poll requests. A nil
// error with a nil body means the request was not made.
type ClaudeQuotaSources struct {
	Usage      []byte
	UsageErr   error
	Profile    *claudeauth.OAuthProfile
	ProfileErr error
}

// ClaudeQuotaObservation builds the poll observation of a Claude OAuth
// credential. The usage payload owns the windows and extra_usage; the profile
// owns the plan. Each section fails on its own. The error is the usage error
// when that request failed: the windows are the poll, the plan only rides along.
func ClaudeQuotaObservation(src ClaudeQuotaSources, now time.Time) (cliproxyauth.QuotaObservation, error) {
	obs := cliproxyauth.QuotaObservation{
		Provider:   cliproxyauth.ProviderClaude,
		Source:     cliproxyauth.SourcePoll,
		ObservedAt: now,
		Sections:   map[cliproxyauth.SectionID]cliproxyauth.SectionStatus{},
	}
	extras := cliproxyauth.ClaudeExtras{}
	if src.UsageErr != nil {
		obs.Sections[cliproxyauth.SectionClaudeUsage] = cliproxyauth.SectionFailed
		extras.ExtraUsage = cliproxyauth.FailedSection[cliproxyauth.ClaudeExtraUsage](src.UsageErr.Error(), now)
	} else {
		obs.Sections[cliproxyauth.SectionClaudeUsage] = cliproxyauth.SectionOK
		root := gjson.ParseBytes(src.Usage)
		obs.Windows = claudeUsageQuotaWindows(root, now)
		extras.ExtraUsage = cliproxyauth.OKSection(claudeExtraUsage(root.Get("extra_usage")), now)
	}
	if src.ProfileErr != nil {
		extras.Plan = cliproxyauth.FailedSection[string](src.ProfileErr.Error(), now)
	} else {
		extras.Plan = cliproxyauth.OKSection(claudePlanType(src.Profile), now)
	}
	obs.Extras = extras
	return obs, src.UsageErr
}

func claudeUsageQuotaWindows(root gjson.Result, now time.Time) []cliproxyauth.QuotaWindowObs {
	windows := make([]cliproxyauth.QuotaWindowObs, 0, len(claudeUsageWindows))
	fable, hasFable := claudeFableLimit(root.Get("limits"))
	for _, spec := range claudeUsageWindows {
		node := root.Get(spec.key)
		if spec.scope != "" && hasFable {
			node = fable
		}
		if !node.IsObject() {
			continue
		}
		window := cliproxyauth.QuotaWindowObs{
			ID:         spec.id,
			ModelScope: spec.scope,
			Period:     spec.period,
			Section:    cliproxyauth.SectionClaudeUsage,
			Source:     cliproxyauth.SourcePoll,
			ObservedAt: now,
		}
		if resetAt, ok := parseQuotaInstant(node.Get("resets_at")); ok {
			window.ResetAt = resetAt
		}
		if used, ok := quotaNumber(firstCodexQuotaResult(node, "utilization", "percent")); ok {
			window.Used = cliproxyauth.KnownPercent(used)
		}
		windows = append(windows, window)
	}
	return windows
}

// claudeFableLimit finds the Fable weekly_scoped entry of limits[]. The active
// one wins; otherwise the first.
func claudeFableLimit(limits gjson.Result) (gjson.Result, bool) {
	if !limits.IsArray() {
		return gjson.Result{}, false
	}
	var first gjson.Result
	found := false
	for _, limit := range limits.Array() {
		if !strings.EqualFold(strings.TrimSpace(limit.Get("kind").String()), "weekly_scoped") {
			continue
		}
		model := strings.ToLower(strings.TrimSpace(limit.Get("scope.model.display_name").String()))
		if model != "fable" && model != "fable 5" {
			continue
		}
		if _, ok := quotaNumber(limit.Get("percent")); !ok {
			continue
		}
		if limit.Get("is_active").Bool() {
			return limit, true
		}
		if !found {
			first, found = limit, true
		}
	}
	return first, found
}

func claudeExtraUsage(node gjson.Result) cliproxyauth.ClaudeExtraUsage {
	usage := cliproxyauth.ClaudeExtraUsage{
		Enabled:      node.Get("is_enabled").Bool(),
		MonthlyLimit: node.Get("monthly_limit").Float(),
		UsedCredits:  node.Get("used_credits").Float(),
	}
	if utilization, ok := quotaNumber(node.Get("utilization")); ok {
		usage.Utilization = cliproxyauth.KnownPercent(utilization)
	}
	return usage
}

// claudePlanType derives the plan label of a profile the way the management
// panel did: an active Team organization, then the personal Max and Pro flags.
// Unknown flags leave the plan empty.
func claudePlanType(profile *claudeauth.OAuthProfile) string {
	if profile == nil {
		return ""
	}
	if strings.EqualFold(strings.TrimSpace(profile.Organization.OrganizationType), "claude_team") &&
		strings.EqualFold(strings.TrimSpace(profile.Organization.SubscriptionStatus), "active") {
		return "plan_team"
	}
	hasMax := claudeProfileFlag(profile.Account.HasClaudeMax)
	if hasMax != nil && *hasMax {
		return "plan_max"
	}
	hasPro := claudeProfileFlag(profile.Account.HasClaudePro)
	if hasPro != nil && *hasPro {
		return "plan_pro"
	}
	if hasMax != nil && hasPro != nil {
		return "plan_free"
	}
	return ""
}

func claudeProfileFlag(value any) *bool {
	switch v := value.(type) {
	case bool:
		return &v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true":
			out := true
			return &out
		case "false":
			out := false
			return &out
		}
	case float64:
		out := v != 0
		return &out
	}
	return nil
}
