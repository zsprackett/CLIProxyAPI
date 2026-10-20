package helps

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	claudeauth "github.com/router-for-me/CLIProxyAPI/v8/internal/auth/claude"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

var claudeTestNow = time.Date(2026, 10, 19, 17, 0, 0, 0, time.UTC)

// The usage payload of a Max account on 2026-10-19: the Fable weekly limit is
// reported in limits[] and iguana_necktie carries an older copy.
const claudeUsageWithLimits = `{
	"five_hour": {"utilization": 23.5, "resets_at": "2026-10-19T20:00:00.123+00:00"},
	"seven_day": {"utilization": 60, "resets_at": "2026-10-22T08:00:00Z"},
	"seven_day_oauth_apps": {"utilization": 0, "resets_at": null},
	"seven_day_opus": {"utilization": 10, "resets_at": "2026-10-22T08:00:00Z"},
	"seven_day_sonnet": {"utilization": 4, "resets_at": "2026-10-22T08:00:00Z"},
	"seven_day_cowork": null,
	"iguana_necktie": {"utilization": 5, "resets_at": "2026-10-20T00:00:00Z"},
	"limits": [
		{"kind": "weekly_scoped", "percent": 12, "resets_at": "2026-10-21T00:00:00Z", "is_active": false, "scope": {"model": {"id": "claude-fable-5", "display_name": "Fable"}}},
		{"kind": "weekly_scoped", "percent": 31.5, "resets_at": "2026-10-21T00:00:00Z", "is_active": true, "scope": {"model": {"id": "claude-fable-5", "display_name": "Fable 5"}}},
		{"kind": "weekly_scoped", "percent": 99, "resets_at": "2026-10-21T00:00:00Z", "is_active": true, "scope": {"model": {"id": "claude-opus-4", "display_name": "Opus"}}}
	],
	"extra_usage": {"is_enabled": true, "monthly_limit": 5000, "used_credits": 1234, "utilization": 24.68}
}`

func claudeProfile(orgType, subscription string, hasMax, hasPro any) *claudeauth.OAuthProfile {
	var profile claudeauth.OAuthProfile
	profile.Account.UUID = "acct"
	profile.Account.HasClaudeMax = hasMax
	profile.Account.HasClaudePro = hasPro
	profile.Organization.OrganizationType = orgType
	profile.Organization.SubscriptionStatus = subscription
	return &profile
}

func windowByID(t *testing.T, obs cliproxyauth.QuotaObservation, id cliproxyauth.WindowID) cliproxyauth.QuotaWindowObs {
	t.Helper()
	for _, w := range obs.Windows {
		if w.ID == id {
			return w
		}
	}
	t.Fatalf("window %q missing from %+v", id, obs.Windows)
	return cliproxyauth.QuotaWindowObs{}
}

func TestClaudeQuotaObservationReadsFableFromLimits(t *testing.T) {
	obs, err := ClaudeQuotaObservation(ClaudeQuotaSources{
		Usage:   []byte(claudeUsageWithLimits),
		Profile: claudeProfile("", "", true, false),
	}, claudeTestNow)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if obs.Provider != cliproxyauth.ProviderClaude || obs.Source != cliproxyauth.SourcePoll || obs.Sections[cliproxyauth.SectionClaudeUsage] != cliproxyauth.SectionOK {
		t.Fatalf("observation header = %+v", obs)
	}
	ids := make([]cliproxyauth.WindowID, 0, len(obs.Windows))
	for _, w := range obs.Windows {
		ids = append(ids, w.ID)
		if w.Section != cliproxyauth.SectionClaudeUsage || w.Source != cliproxyauth.SourcePoll || !w.ObservedAt.Equal(claudeTestNow) {
			t.Fatalf("window %s = %+v", w.ID, w)
		}
	}
	wantIDs := []cliproxyauth.WindowID{"five_hour", "seven_day", "seven_day_oauth_apps", "seven_day_opus", "seven_day_sonnet", "seven_day_fable"}
	if len(ids) != len(wantIDs) {
		t.Fatalf("window ids = %v, want %v", ids, wantIDs)
	}
	for i := range wantIDs {
		if ids[i] != wantIDs[i] {
			t.Fatalf("window ids = %v, want %v", ids, wantIDs)
		}
	}
	fable := windowByID(t, obs, "seven_day_fable")
	if used, _ := fable.Used.Get(); used != 31.5 || fable.ModelScope != "fable" || fable.Class(cliproxyauth.ProviderClaude) != "claude:7d_oi" {
		t.Fatalf("fable window = %+v, want the active limits[] entry at 31.5", fable)
	}
	if !fable.ResetAt.Equal(time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)) || fable.Period != 7*24*time.Hour {
		t.Fatalf("fable reset = %v period = %v", fable.ResetAt, fable.Period)
	}
	fiveHour := windowByID(t, obs, "five_hour")
	if used, _ := fiveHour.Used.Get(); used != 23.5 || fiveHour.Class(cliproxyauth.ProviderClaude) != "claude:5h" {
		t.Fatalf("five_hour = %+v", fiveHour)
	}
	if opus := windowByID(t, obs, "seven_day_opus"); opus.Class(cliproxyauth.ProviderClaude) != "" {
		t.Fatalf("seven_day_opus is display-only, got class %q", opus.Class(cliproxyauth.ProviderClaude))
	}
	extras := obs.Extras.(cliproxyauth.ClaudeExtras)
	if extras.Plan.Status != cliproxyauth.SectionOK || *extras.Plan.Value != "plan_max" {
		t.Fatalf("plan = %+v", extras.Plan)
	}
	usage := extras.ExtraUsage.Value
	if extras.ExtraUsage.Status != cliproxyauth.SectionOK || usage == nil || !usage.Enabled || usage.MonthlyLimit != 5000 || usage.UsedCredits != 1234 {
		t.Fatalf("extra usage = %+v", extras.ExtraUsage)
	}
	if utilization, known := usage.Utilization.Get(); !known || utilization != 24.68 {
		t.Fatalf("extra usage utilization = %v known %t", utilization, known)
	}
}

func TestClaudeQuotaObservationFallsBackToIguanaNecktie(t *testing.T) {
	obs, err := ClaudeQuotaObservation(ClaudeQuotaSources{
		Usage: []byte(`{"five_hour": {"utilization": 1, "resets_at": null}, "iguana_necktie": {"utilization": 5, "resets_at": "2026-10-20T00:00:00Z"},
			"limits": [{"kind": "weekly_scoped", "percent": 99, "scope": {"model": {"display_name": "Opus"}}}]}`),
		Profile: claudeProfile("claude_team", "active", nil, nil),
	}, claudeTestNow)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	fable := windowByID(t, obs, "seven_day_fable")
	if used, _ := fable.Used.Get(); used != 5 || fable.ModelScope != "fable" || !fable.ResetAt.Equal(time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("fable window = %+v, want iguana_necktie at 5", fable)
	}
	if len(obs.Windows) != 2 {
		t.Fatalf("windows = %+v, want five_hour and seven_day_fable", obs.Windows)
	}
	if plan := obs.Extras.(cliproxyauth.ClaudeExtras).Plan; *plan.Value != "plan_team" {
		t.Fatalf("plan = %+v, want plan_team", plan)
	}
}

func TestClaudeQuotaObservationKeepsNullUtilizationUnknown(t *testing.T) {
	obs, err := ClaudeQuotaObservation(ClaudeQuotaSources{
		Usage:   []byte(`{"five_hour": {"utilization": null, "resets_at": "2026-10-19T20:00:00Z"}, "extra_usage": {"is_enabled": false, "utilization": null}}`),
		Profile: claudeProfile("", "", false, false),
	}, claudeTestNow)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	fiveHour := windowByID(t, obs, "five_hour")
	if _, known := fiveHour.Used.Get(); known {
		t.Fatalf("null utilization became known: %+v", fiveHour)
	}
	if fiveHour.ResetAt.IsZero() {
		t.Fatalf("reset time lost: %+v", fiveHour)
	}
	extras := obs.Extras.(cliproxyauth.ClaudeExtras)
	if _, known := extras.ExtraUsage.Value.Utilization.Get(); known || extras.ExtraUsage.Value.Enabled {
		t.Fatalf("extra usage = %+v", extras.ExtraUsage)
	}
	if *extras.Plan.Value != "plan_free" {
		t.Fatalf("plan = %+v, want plan_free", extras.Plan)
	}
	raw, _ := json.Marshal(fiveHour.Used)
	if string(raw) != "null" {
		t.Fatalf("unknown percent marshals as %s", raw)
	}
}

func TestClaudeQuotaObservationProfileFailureMarksOnlyPlanFailed(t *testing.T) {
	obs, err := ClaudeQuotaObservation(ClaudeQuotaSources{
		Usage:      []byte(claudeUsageWithLimits),
		ProfileErr: errors.New("fetch Claude OAuth profile failed with status 503"),
	}, claudeTestNow)
	if err != nil {
		t.Fatalf("a profile failure alone is not a poll failure, got %v", err)
	}
	if obs.Sections[cliproxyauth.SectionClaudeUsage] != cliproxyauth.SectionOK || len(obs.Windows) != 6 {
		t.Fatalf("usage section = %+v windows = %d", obs.Sections, len(obs.Windows))
	}
	extras := obs.Extras.(cliproxyauth.ClaudeExtras)
	if extras.Plan.Status != cliproxyauth.SectionFailed || extras.Plan.Value != nil || extras.Plan.Error != "fetch Claude OAuth profile failed with status 503" || !extras.Plan.CheckedAt.Equal(claudeTestNow) {
		t.Fatalf("plan = %+v, want failed with the error", extras.Plan)
	}
	if extras.ExtraUsage.Status != cliproxyauth.SectionOK {
		t.Fatalf("extra usage = %+v, want ok", extras.ExtraUsage)
	}
}

func TestClaudeQuotaObservationUsageFailureKeepsPlan(t *testing.T) {
	obs, err := ClaudeQuotaObservation(ClaudeQuotaSources{
		UsageErr: errors.New("fetch Claude OAuth usage failed with status 429"),
		Profile:  claudeProfile("", "", false, true),
	}, claudeTestNow)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if obs.Sections[cliproxyauth.SectionClaudeUsage] != cliproxyauth.SectionFailed || len(obs.Windows) != 0 {
		t.Fatalf("usage section = %+v windows = %+v", obs.Sections, obs.Windows)
	}
	extras := obs.Extras.(cliproxyauth.ClaudeExtras)
	if extras.ExtraUsage.Status != cliproxyauth.SectionFailed || extras.ExtraUsage.Error != "fetch Claude OAuth usage failed with status 429" {
		t.Fatalf("extra usage = %+v, want failed", extras.ExtraUsage)
	}
	if *extras.Plan.Value != "plan_pro" {
		t.Fatalf("plan = %+v, want plan_pro", extras.Plan)
	}

	_, err = ClaudeQuotaObservation(ClaudeQuotaSources{UsageErr: errors.New("usage down"), ProfileErr: errors.New("profile down")}, claudeTestNow)
	if err == nil {
		t.Fatal("both sections failed but the poll reported success")
	}
}

func TestClaudePlanType(t *testing.T) {
	cases := map[string]struct {
		profile *claudeauth.OAuthProfile
		want    string
	}{
		"nil":                 {nil, ""},
		"team active":         {claudeProfile("claude_team", "active", true, true), "plan_team"},
		"team inactive max":   {claudeProfile("claude_team", "canceled", true, false), "plan_max"},
		"pro":                 {claudeProfile("", "", false, true), "plan_pro"},
		"free":                {claudeProfile("", "", false, false), "plan_free"},
		"string flags":        {claudeProfile("", "", "false", "true"), "plan_pro"},
		"unknown flags":       {claudeProfile("", "", nil, nil), ""},
		"one unknown is open": {claudeProfile("", "", false, nil), ""},
	}
	for name, tc := range cases {
		if got := claudePlanType(tc.profile); got != tc.want {
			t.Errorf("%s: plan = %q, want %q", name, got, tc.want)
		}
	}
}
