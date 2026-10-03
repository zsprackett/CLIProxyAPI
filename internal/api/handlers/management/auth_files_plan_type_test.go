package management

import (
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestAuthClaudePlanType(t *testing.T) {
	cases := []struct {
		name string
		auth *coreauth.Auth
		want string
	}{
		{name: "nil auth", auth: nil, want: ""},
		{
			name: "claude plan",
			auth: &coreauth.Auth{Provider: "claude", Metadata: map[string]any{"plan_type": " Max "}},
			want: "max",
		},
		{
			name: "claude without plan",
			auth: &coreauth.Auth{Provider: "claude", Metadata: map[string]any{"email": "user@example.com"}},
			want: "",
		},
		{
			name: "codex plan stays in id_token claims",
			auth: &coreauth.Auth{Provider: "codex", Metadata: map[string]any{"plan_type": "plus"}},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := authClaudePlanType(tc.auth); got != tc.want {
				t.Fatalf("authClaudePlanType() = %q, want %q", got, tc.want)
			}
		})
	}
}
