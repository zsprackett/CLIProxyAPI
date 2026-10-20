package helps

import (
	"errors"
	"testing"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func codexResetCreditsOf(t *testing.T, obs cliproxyauth.QuotaObservation) cliproxyauth.Section[cliproxyauth.CodexResetCredits] {
	t.Helper()
	extras, ok := obs.Extras.(cliproxyauth.CodexExtras)
	if !ok {
		t.Fatalf("extras = %T, want CodexExtras", obs.Extras)
	}
	return extras.ResetCredits
}

func intPtrValue(p *int) int {
	if p == nil {
		return -1
	}
	return *p
}

// Only an available codex_rate_limits credit with an expiry is a credit the
// user can spend; consumed, expired, other-feature, and open-ended ones are not listed.
func TestCodexResetCreditsKeepOnlyAvailableCreditsWithExpiry(t *testing.T) {
	body := []byte(`{"available_count": 1, "credits": [
		{"id": "rc_a", "status": "available", "reset_type": "codex_rate_limits", "expires_at": "2026-12-01T00:00:00Z"},
		{"id": "rc_b", "status": "consumed", "reset_type": "codex_rate_limits", "expires_at": "2026-12-01T00:00:00Z"},
		{"id": "rc_c", "status": "expired", "reset_type": "codex_rate_limits", "expires_at": "2026-09-01T00:00:00Z"},
		{"id": "rc_d", "status": "available", "reset_type": "codex_rate_limits"},
		{"id": "rc_e", "status": "available", "reset_type": "other_feature", "expires_at": "2026-12-01T00:00:00Z"}]}`)
	obs, err := CodexQuotaObservation(CodexQuotaSources{Usage: readCodexFixture(t, "codex_usage.json"), ResetCredits: body}, codexTestNow)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	reset := codexResetCreditsOf(t, obs)
	if reset.Status != cliproxyauth.SectionOK || len(reset.Value.Credits) != 1 || reset.Value.Credits[0].ID != "rc_a" {
		t.Fatalf("reset credits = %+v, want only rc_a", reset)
	}
	if intPtrValue(reset.Value.Available) != 1 {
		t.Fatalf("available = %d, want the payload count 1", intPtrValue(reset.Value.Available))
	}
}

// The counts come from the details payload, then from the listed credits,
// then from rate_limit_reset_credits in the usage payload.
func TestCodexResetCreditCountsFallBack(t *testing.T) {
	usageWithCounts := []byte(`{"plan_type": "pro", "rate_limit": {"primary_window": {"used_percent": 1, "limit_window_seconds": 18000, "reset_after_seconds": 60}},
		"rate_limit_reset_credits": {"available_count": 1, "applicable_available_count": 0}}`)
	twoCreditsNoCounts := []byte(`{"credits": [
		{"id": "rc_a", "status": "available", "reset_type": "codex_rate_limits", "expires_at": "2026-12-01T00:00:00Z"},
		{"id": "rc_b", "status": "available", "reset_type": "codex_rate_limits", "expires_at": "2026-12-02T00:00:00Z"}]}`)
	cases := map[string]struct {
		src                           CodexQuotaSources
		wantStatus                    cliproxyauth.SectionStatus
		wantAvailable, wantApplicable int
		wantCredits                   int
	}{
		"listed credits count when the details carry no counts": {
			src:        CodexQuotaSources{Usage: readCodexFixture(t, "codex_usage.json"), ResetCredits: twoCreditsNoCounts},
			wantStatus: cliproxyauth.SectionOK, wantAvailable: 2, wantApplicable: 2, wantCredits: 2,
		},
		"usage counts fill empty details": {
			src:        CodexQuotaSources{Usage: usageWithCounts, ResetCredits: []byte(`{"credits": []}`)},
			wantStatus: cliproxyauth.SectionOK, wantAvailable: 1, wantApplicable: 0, wantCredits: 0,
		},
		"usage counts survive a failed details request": {
			src:        CodexQuotaSources{Usage: usageWithCounts, ResetCreditsErr: errors.New("fetch /backend-api/wham/rate-limit-reset-credits failed with status 500")},
			wantStatus: cliproxyauth.SectionFailed, wantAvailable: 1, wantApplicable: 0, wantCredits: 0,
		},
		"no counts anywhere stay unknown": {
			src:        CodexQuotaSources{Usage: readCodexFixture(t, "codex_usage.json"), ResetCredits: []byte(`{"credits": []}`)},
			wantStatus: cliproxyauth.SectionOK, wantAvailable: -1, wantApplicable: -1, wantCredits: 0,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			obs, err := CodexQuotaObservation(tc.src, codexTestNow)
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			reset := codexResetCreditsOf(t, obs)
			if reset.Status != tc.wantStatus || reset.Value == nil {
				t.Fatalf("reset credits = %+v, want status %s with a value", reset, tc.wantStatus)
			}
			if got := intPtrValue(reset.Value.Available); got != tc.wantAvailable {
				t.Fatalf("available = %d, want %d", got, tc.wantAvailable)
			}
			if got := intPtrValue(reset.Value.ApplicableAvailable); got != tc.wantApplicable {
				t.Fatalf("applicable = %d, want %d", got, tc.wantApplicable)
			}
			if len(reset.Value.Credits) != tc.wantCredits {
				t.Fatalf("credits = %+v, want %d", reset.Value.Credits, tc.wantCredits)
			}
		})
	}
}

// A credit balance is shown only when it is a plain decimal number.
func TestCodexCreditsBalanceMustBeDecimal(t *testing.T) {
	cases := map[string]struct {
		raw  string
		want string
	}{
		"decimal string": {`"42.50"`, "42.50"},
		"integer number": {`12`, "12"},
		"words":          {`"abc"`, ""},
		"exponent":       {`"1e3"`, ""},
		"negative":       {`"-5"`, ""},
		"null":           {`null`, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"rate_limit": {"primary_window": {"used_percent": 1, "limit_window_seconds": 18000, "reset_after_seconds": 60}}, "credits": {"has_credits": true, "balance": ` + tc.raw + `}}`)
			obs, err := CodexQuotaObservation(CodexQuotaSources{Usage: body}, codexTestNow)
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			credits := obs.Extras.(cliproxyauth.CodexExtras).Credits.Value
			got := ""
			if credits.Balance != nil {
				got = *credits.Balance
			}
			if got != tc.want {
				t.Fatalf("balance = %q, want %q", got, tc.want)
			}
		})
	}
}

// allowed:false without a percent reads as limit reached, like limit_reached;
// a known percent still wins.
func TestCodexAllowedFalseWithoutPercentMarksLimitReached(t *testing.T) {
	body := []byte(`{"rate_limit": {"allowed": false,
		"primary_window": {"limit_window_seconds": 18000, "reset_after_seconds": 600},
		"secondary_window": {"used_percent": 40, "limit_window_seconds": 604800, "reset_after_seconds": 7200}},
		"code_review_rate_limit": {"allowed": true,
		"primary_window": {"limit_window_seconds": 18000}}}`)
	obs, err := CodexQuotaObservation(CodexQuotaSources{Usage: body}, codexTestNow)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	primary := windowByID(t, obs, "primary")
	if _, known := primary.Used.Get(); known || !primary.LimitReached {
		t.Fatalf("primary = %+v, want limit reached with unknown percent", primary)
	}
	if used, known := windowByID(t, obs, "secondary").Used.Get(); !known || used != 40 {
		t.Fatalf("secondary used = %v %v, want the known 40", used, known)
	}
	for _, w := range obs.Windows {
		if w.ID == "code_review.primary" {
			t.Fatalf("an allowed window with no percent and no reset was kept: %+v", w)
		}
	}
	var snapshot cliproxyauth.QuotaSnapshot
	snapshot.Observe(obs)
	for _, w := range snapshot.RoutingWindows(codexTestNow) {
		if w.Class == "codex:5h" && w.UsedPercent != 100 {
			t.Fatalf("routing reads allowed:false as %v, want 100", w.UsedPercent)
		}
	}
}

// An additional limit with no name is still shown, as additional-N.
func TestCodexAdditionalLimitWithoutNameIsKept(t *testing.T) {
	body := []byte(`{"rate_limit": {"primary_window": {"used_percent": 1, "limit_window_seconds": 18000, "reset_after_seconds": 60}},
		"additional_rate_limits": [
			{"limit_name": "Spark", "rate_limit": {"primary_window": {"used_percent": 3, "limit_window_seconds": 18000, "reset_after_seconds": 60}}},
			{"rate_limit": {"primary_window": {"used_percent": 7, "limit_window_seconds": 18000, "reset_after_seconds": 60}}}]}`)
	obs, err := CodexQuotaObservation(CodexQuotaSources{Usage: body}, codexTestNow)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	named := windowByID(t, obs, "additional.Spark.primary")
	if named.Label != "Spark" {
		t.Fatalf("named window = %+v, want label Spark", named)
	}
	unnamed := windowByID(t, obs, "additional.additional-2.primary")
	if used, _ := unnamed.Used.Get(); unnamed.Label != "additional-2" || used != 7 {
		t.Fatalf("unnamed window = %+v, want label additional-2 at 7 percent", unnamed)
	}
}
