package claude

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestOAuthProfilePlanType(t *testing.T) {
	cases := []struct {
		name    string
		profile string
		want    string
	}{
		{
			name:    "active team organization",
			profile: `{"account":{"has_claude_max":true},"organization":{"organization_type":"claude_team","subscription_status":"active"}}`,
			want:    PlanTypeTeam,
		},
		{
			name:    "inactive team falls back to account flags",
			profile: `{"account":{"has_claude_max":false,"has_claude_pro":true},"organization":{"organization_type":"claude_team","subscription_status":"past_due"}}`,
			want:    PlanTypePro,
		},
		{
			name:    "max",
			profile: `{"account":{"has_claude_max":true,"has_claude_pro":false}}`,
			want:    PlanTypeMax,
		},
		{
			name:    "string flags",
			profile: `{"account":{"has_claude_max":"false","has_claude_pro":"1"}}`,
			want:    PlanTypePro,
		},
		{
			name:    "both flags false is free",
			profile: `{"account":{"has_claude_max":false,"has_claude_pro":false}}`,
			want:    PlanTypeFree,
		},
		{
			name:    "missing flags are unknown",
			profile: `{"account":{"uuid":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}}`,
			want:    "",
		},
		{
			name:    "one known false flag is unknown",
			profile: `{"account":{"has_claude_max":false}}`,
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var profile OAuthProfile
			if errUnmarshal := json.Unmarshal([]byte(tc.profile), &profile); errUnmarshal != nil {
				t.Fatalf("unmarshal profile: %v", errUnmarshal)
			}
			if got := profile.PlanType(); got != tc.want {
				t.Fatalf("PlanType() = %q, want %q", got, tc.want)
			}
		})
	}

	var nilProfile *OAuthProfile
	if got := nilProfile.PlanType(); got != "" {
		t.Fatalf("nil PlanType() = %q, want empty", got)
	}
}

func TestOAuthProfileToleratesUnexpectedFlagTypes(t *testing.T) {
	var profile OAuthProfile
	payload := `{"account":{"uuid":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","has_claude_max":{"tier":"x"},"has_claude_pro":[1]}}`
	if errUnmarshal := json.Unmarshal([]byte(payload), &profile); errUnmarshal != nil {
		t.Fatalf("unexpected flag types must not fail the identity lookup: %v", errUnmarshal)
	}
	if profile.Account.UUID == "" {
		t.Fatal("account UUID was not decoded")
	}
	if got := profile.PlanType(); got != "" {
		t.Fatalf("PlanType() = %q, want empty for unreadable flags", got)
	}
}

func TestRefreshTokensRecordsPlanType(t *testing.T) {
	resetClaudeRefreshState()
	defer resetClaudeRefreshState()

	auth := &ClaudeAuth{
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				body := `{"access_token":"new-access","expires_in":3600}`
				if req.URL.String() == ProfileURL {
					body = `{
						"account":{"uuid":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa","email":"user@example.com","has_claude_max":true,"has_claude_pro":false},
						"organization":{"uuid":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","name":"Example Org"}
					}`
				}
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(strings.NewReader(body)),
					Header:     make(http.Header),
					Request:    req,
				}, nil
			}),
		},
	}

	tokenData, errRefresh := auth.RefreshTokens(t.Context(), "placeholder-refresh")
	if errRefresh != nil {
		t.Fatalf("RefreshTokens() error = %v", errRefresh)
	}
	if tokenData.PlanType != PlanTypeMax {
		t.Fatalf("PlanType = %q, want %q", tokenData.PlanType, PlanTypeMax)
	}

	storage := &ClaudeTokenStorage{PlanType: PlanTypeTeam}
	auth.UpdateTokenStorage(storage, &ClaudeTokenData{AccessToken: "a"})
	if storage.PlanType != PlanTypeTeam {
		t.Fatalf("storage PlanType = %q, want preserved %q when refresh omits it", storage.PlanType, PlanTypeTeam)
	}
}
