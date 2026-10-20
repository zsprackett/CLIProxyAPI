package helps

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

var codexTestNow = time.Date(2026, 10, 19, 17, 0, 0, 0, time.UTC)

// codexGoldenPath is the snapshot the recorded Codex payloads produce. The
// management panel reads the same file in its card tests.
const codexGoldenPath = "../../../../sdk/cliproxy/auth/testdata/quota_snapshot_codex.json"

func readCodexFixture(t *testing.T, name string) []byte {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return body
}

func recordedCodexSources(t *testing.T) CodexQuotaSources {
	t.Helper()
	return CodexQuotaSources{
		Usage:        readCodexFixture(t, "codex_usage.json"),
		Subscription: readCodexFixture(t, "codex_subscription.json"),
		ResetCredits: readCodexFixture(t, "codex_reset_credits.json"),
	}
}

func TestCodexQuotaObservationMatchesGolden(t *testing.T) {
	obs, err := CodexQuotaObservation(recordedCodexSources(t), codexTestNow)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	obs.Poll = &cliproxyauth.PollStatus{At: codexTestNow, Trigger: cliproxyauth.PollBackground}
	var snapshot cliproxyauth.QuotaSnapshot
	if !snapshot.Observe(obs) {
		t.Fatal("observation changed nothing")
	}
	got, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got = append(got, '\n')
	if os.Getenv("QUOTA_GOLDEN_UPDATE") == "1" {
		if err := os.WriteFile(codexGoldenPath, got, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(codexGoldenPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("snapshot JSON differs from %s\n--- got ---\n%s\n--- want ---\n%s", codexGoldenPath, got, want)
	}
}

func TestCodexQuotaObservationReadsWindowsAndExtras(t *testing.T) {
	obs, err := CodexQuotaObservation(recordedCodexSources(t), codexTestNow)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if obs.Sections[cliproxyauth.SectionCodexUsage] != cliproxyauth.SectionOK {
		t.Fatalf("sections = %+v", obs.Sections)
	}
	wantIDs := []cliproxyauth.WindowID{"primary", "secondary", "code_review.primary", "code_review.secondary", "additional.bengalfox.primary"}
	if len(obs.Windows) != len(wantIDs) {
		t.Fatalf("windows = %+v, want ids %v", obs.Windows, wantIDs)
	}
	for i, w := range obs.Windows {
		if w.ID != wantIDs[i] || w.Section != cliproxyauth.SectionCodexUsage || w.Source != cliproxyauth.SourcePoll {
			t.Fatalf("window %d = %+v, want id %s", i, w, wantIDs[i])
		}
	}
	primary := windowByID(t, obs, "primary")
	if used, _ := primary.Used.Get(); used != 12 || primary.Period != 5*time.Hour || primary.Class(cliproxyauth.ProviderCodex) != "codex:5h" || !primary.ResetAt.Equal(time.Unix(1792432800, 0)) {
		t.Fatalf("primary = %+v", primary)
	}
	if secondary := windowByID(t, obs, "secondary"); secondary.Class(cliproxyauth.ProviderCodex) != "codex:weekly" {
		t.Fatalf("secondary = %+v, want codex:weekly", secondary)
	}
	if review := windowByID(t, obs, "code_review.primary"); review.Class(cliproxyauth.ProviderCodex) != "" || review.Label != "" {
		t.Fatalf("code review window = %+v, want display-only", review)
	}
	if extra := windowByID(t, obs, "additional.bengalfox.primary"); extra.Label != "bengalfox" || extra.Class(cliproxyauth.ProviderCodex) != "" {
		t.Fatalf("additional window = %+v", extra)
	}
	extras := obs.Extras.(cliproxyauth.CodexExtras)
	if *extras.Plan.Value != "pro" || !*extras.Access.Value.Allowed || *extras.Access.Value.LimitReached {
		t.Fatalf("plan = %+v access = %+v", extras.Plan, extras.Access)
	}
	if credits := extras.Credits.Value; !*credits.HasCredits || *credits.Unlimited || *credits.Balance != "42.50" {
		t.Fatalf("credits = %+v", extras.Credits)
	}
	if !extras.Subscription.Value.ActiveUntil.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("subscription = %+v", extras.Subscription)
	}
	reset := extras.ResetCredits.Value
	if *reset.Available != 2 || *reset.ApplicableAvailable != 1 || len(reset.Credits) != 2 || reset.Credits[0].ID != "rc_1" {
		t.Fatalf("reset credits = %+v, want 2 codex_rate_limits credits", extras.ResetCredits)
	}
}

func TestCodexQuotaObservationClassesWindowsByPeriod(t *testing.T) {
	cases := map[string]struct {
		seconds int
		want    cliproxyauth.WindowClass
	}{
		"300 minutes":   {18000, "codex:5h"},
		"10080 minutes": {604800, "codex:weekly"},
		"43200 minutes": {2592000, "codex:monthly"},
		"no period":     {0, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"rate_limit": {"primary_window": {"used_percent": 10, "limit_window_seconds": ` + itoa(tc.seconds) + `, "reset_at": 1792000800}}}`)
			obs, err := CodexQuotaObservation(CodexQuotaSources{Usage: body}, codexTestNow)
			if err != nil {
				t.Fatalf("error = %v", err)
			}
			if got := windowByID(t, obs, "primary").Class(cliproxyauth.ProviderCodex); got != tc.want {
				t.Fatalf("class = %q, want %q", got, tc.want)
			}
		})
	}
}

func itoa(v int) string {
	raw, _ := json.Marshal(v)
	return string(raw)
}

func TestCodexQuotaObservationLimitReachedWithoutPercent(t *testing.T) {
	body := []byte(`{"rate_limit": {"allowed": false, "limit_reached": true,
		"primary_window": {"limit_window_seconds": 18000, "reset_after_seconds": 600},
		"secondary_window": {"used_percent": 100, "limit_window_seconds": 604800, "reset_after_seconds": 7200}}}`)
	obs, err := CodexQuotaObservation(CodexQuotaSources{Usage: body}, codexTestNow)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	primary := windowByID(t, obs, "primary")
	if _, known := primary.Used.Get(); known || !primary.LimitReached {
		t.Fatalf("primary = %+v, want limit reached with unknown percent", primary)
	}
	if !primary.ResetAt.Equal(codexTestNow.Add(10 * time.Minute)) {
		t.Fatalf("primary reset = %v, want now + reset_after_seconds", primary.ResetAt)
	}
	var snapshot cliproxyauth.QuotaSnapshot
	snapshot.Observe(obs)
	for _, w := range snapshot.RoutingWindows(codexTestNow) {
		if w.Class == "codex:5h" && w.UsedPercent != 100 {
			t.Fatalf("routing reads limit reached as %v, want 100", w.UsedPercent)
		}
	}
	access := obs.Extras.(cliproxyauth.CodexExtras).Access.Value
	if *access.Allowed || !*access.LimitReached {
		t.Fatalf("access = %+v", access)
	}
}

func TestCodexQuotaObservationResetCreditsFailureKeepsOldCredits(t *testing.T) {
	first, err := CodexQuotaObservation(recordedCodexSources(t), codexTestNow)
	if err != nil {
		t.Fatalf("first poll error = %v", err)
	}
	var snapshot cliproxyauth.QuotaSnapshot
	snapshot.Observe(first)

	later := codexTestNow.Add(5 * time.Minute)
	src := recordedCodexSources(t)
	src.ResetCredits, src.ResetCreditsErr = nil, errors.New("fetch /backend-api/wham/rate-limit-reset-credits failed with status 500")
	second, err := CodexQuotaObservation(src, later)
	if err != nil {
		t.Fatalf("a reset-credits failure alone is not a poll failure, got %v", err)
	}
	snapshot.Observe(second)
	reset := snapshot.Extras.(cliproxyauth.CodexExtras).ResetCredits
	if reset.Status != cliproxyauth.SectionFailed || reset.Value == nil || *reset.Value.Available != 2 {
		t.Fatalf("reset credits = %+v, want failed with the old value", reset)
	}
	if !reset.ValueAt.Equal(codexTestNow) || !reset.CheckedAt.Equal(later) || reset.Error == "" {
		t.Fatalf("reset credits times = value %v checked %v error %q", reset.ValueAt, reset.CheckedAt, reset.Error)
	}
	if plan := snapshot.Extras.(cliproxyauth.CodexExtras).Plan; plan.Status != cliproxyauth.SectionOK || !plan.ValueAt.Equal(later) {
		t.Fatalf("plan = %+v, want refreshed", plan)
	}
}

// The usage request owns the windows, so its failure fails the poll even when
// the other sections succeeded; those sections still land.
func TestCodexQuotaObservationSectionsStandAlone(t *testing.T) {
	obs, err := CodexQuotaObservation(CodexQuotaSources{UsageErr: errors.New("usage 503"), ResetCredits: readCodexFixture(t, "codex_reset_credits.json")}, codexTestNow)
	if err == nil || err.Error() != "usage 503" {
		t.Fatalf("error = %v, want the usage error", err)
	}
	if obs.Sections[cliproxyauth.SectionCodexUsage] != cliproxyauth.SectionFailed || len(obs.Windows) != 0 {
		t.Fatalf("sections = %+v windows = %+v", obs.Sections, obs.Windows)
	}
	extras := obs.Extras.(cliproxyauth.CodexExtras)
	if extras.Access.Status != cliproxyauth.SectionFailed || extras.Credits.Status != cliproxyauth.SectionFailed || extras.Plan.Status != cliproxyauth.SectionFailed {
		t.Fatalf("usage sections = plan %s access %s credits %s, want failed", extras.Plan.Status, extras.Access.Status, extras.Credits.Status)
	}
	if extras.Subscription.Status != "" {
		t.Fatalf("subscription was not requested but has status %q", extras.Subscription.Status)
	}
	if extras.ResetCredits.Status != cliproxyauth.SectionOK {
		t.Fatalf("reset credits = %+v", extras.ResetCredits)
	}

	_, err = CodexQuotaObservation(CodexQuotaSources{UsageErr: errors.New("usage 503"), ResetCreditsErr: errors.New("credits 503")}, codexTestNow)
	if err == nil {
		t.Fatal("no section succeeded but the poll reported success")
	}
	obs, err = CodexQuotaObservation(CodexQuotaSources{Usage: readCodexFixture(t, "codex_usage.json"), ResetCredits: []byte(`{"unexpected": true}`)}, codexTestNow)
	if err != nil {
		t.Fatalf("error = %v", err)
	}
	if reset := obs.Extras.(cliproxyauth.CodexExtras).ResetCredits; reset.Status != cliproxyauth.SectionFailed {
		t.Fatalf("malformed reset credits payload = %+v, want failed", reset)
	}
}
