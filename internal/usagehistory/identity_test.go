package usagehistory

import "testing"

func TestHashClientKeyMatchesThePanel(t *testing.T) {
	// The same vectors as web/management-center/tests/fixtures/quotaForecast.ts.
	cases := map[string]string{
		"sk-team-alpha":     "4e9f7b2de42c",
		"  sk-solo-beta \n": "c81f65615e76",
		"   ":               "-",
		"":                  "-",
	}
	for key, want := range cases {
		if got := HashClientKey(key); got != want {
			t.Fatalf("HashClientKey(%q) = %q, want %q", key, got, want)
		}
	}
	if HashClientKey("k") == HashClientKey("j") {
		t.Fatal("different keys share a hash")
	}
}

func TestAgentFamily(t *testing.T) {
	cases := map[string]string{
		"claude-cli/2.1.3 (external, cli)": "claude-cli",
		"Codex_CLI_RS/0.50":                "codex_cli_rs",
		"curl":                             "curl",
		"  ":                               "-",
		"":                                 "-",
		"averyveryveryveryveryverylongproductnameover40": "averyveryveryveryveryverylongproductname",
	}
	for userAgent, want := range cases {
		if got := AgentFamily(userAgent); got != want {
			t.Fatalf("AgentFamily(%q) = %q, want %q", userAgent, got, want)
		}
	}
}
