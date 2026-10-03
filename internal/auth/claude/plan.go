package claude

import (
	"strings"
)

// Claude subscription plans recorded on a credential as metadata "plan_type".
const (
	PlanTypeTeam = "team"
	PlanTypeMax  = "max"
	PlanTypePro  = "pro"
	PlanTypeFree = "free"
)

// PlanType derives the subscription plan from the OAuth profile. An active Team
// organization wins because its token is scoped to the organization; otherwise
// the personal account flags decide. It returns "" when the profile does not
// say, so callers never overwrite a known plan with a guess.
func (p *OAuthProfile) PlanType() string {
	if p == nil {
		return ""
	}
	organizationType := strings.ToLower(strings.TrimSpace(p.Organization.OrganizationType))
	subscriptionStatus := strings.ToLower(strings.TrimSpace(p.Organization.SubscriptionStatus))
	if organizationType == "claude_team" && subscriptionStatus == "active" {
		return PlanTypeTeam
	}
	hasMax, knownMax := profileFlag(p.Account.HasClaudeMax)
	if knownMax && hasMax {
		return PlanTypeMax
	}
	hasPro, knownPro := profileFlag(p.Account.HasClaudePro)
	if knownPro && hasPro {
		return PlanTypePro
	}
	if knownMax && knownPro {
		return PlanTypeFree
	}
	return ""
}

// profileFlag reads a loosely typed profile flag. The fields are decoded as any
// so an unexpected representation can never fail the identity lookup itself.
func profileFlag(value any) (flag bool, known bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case float64:
		return typed != 0, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "y", "on":
			return true, true
		case "false", "0", "no", "n", "off":
			return false, true
		}
	}
	return false, false
}
