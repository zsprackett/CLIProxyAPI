package quotaforecast

import (
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func claudeAuth(id string, utilization float64, observed, reset time.Time) *coreauth.Auth {
	auth := &coreauth.Auth{
		ID:       id,
		Provider: "claude",
		FileName: "/auths/" + id + ".json",
	}
	auth.QuotaSnapshot.ObserveResponseHeaders("claude", "", http.Header{
		"Anthropic-Ratelimit-Unified-7d-Utilization": []string{strconv.FormatFloat(utilization, 'f', -1, 64)},
		"Anthropic-Ratelimit-Unified-7d-Reset":       []string{strconv.FormatInt(reset.Unix(), 10)},
	}, observed)
	return auth
}

func testService(now *time.Time) *Service {
	s := newService()
	s.now = func() time.Time { return *now }
	s.notify = func(*config.Config, alertRecord, time.Time) {}
	return s
}

func TestForecastEndToEnd(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{}
	cfg.AuthDir = dir
	cfg.QuotaForecast.Enabled = true
	cfg.APIKeys = []string{"sk-client-secret-1234"}
	now := testNow
	s := testService(&now)
	reset := testNow.Add(72 * time.Hour)

	if resp := s.Forecast(t.Context(), &config.Config{}, nil, ForecastOptions{}); resp.Enabled || resp.Reason != "disabled" {
		t.Fatalf("disabled response = %+v", resp)
	}

	usage := enabledUsage()
	useFakeUsage(t, usage)
	for i := 0; i <= 12; i++ {
		used := 0.26 + 0.02*float64(i)
		auths := []*coreauth.Auth{claudeAuth("a", used, now, reset), claudeAuth("b", used, now, reset)}
		usage.result.Rows = append(usage.result.Rows, usagehistory.HourlyRow{
			Hour: now.Truncate(time.Hour).Unix(), Provider: "claude", AuthID: "a", Model: "claude-sonnet-5-5",
			KeyHash: usagehistory.HashClientKey(cfg.APIKeys[0]), Agent: usagehistory.AgentFamily(""),
			Requests: 1, Tokens: usagehistory.Tokens{Uncached: 1000},
		})
		s.Forecast(t.Context(), cfg, auths, ForecastOptions{})
		now = now.Add(time.Hour)
	}
	now = now.Add(-time.Hour)
	auths := []*coreauth.Auth{claudeAuth("a", 0.5, now, reset), claudeAuth("b", 0.5, now, reset)}

	summary := s.Forecast(t.Context(), cfg, auths, ForecastOptions{})
	if !summary.Enabled || len(summary.Pools) != 1 || summary.Pools[0].Chart != nil {
		t.Fatalf("summary = %+v", summary)
	}
	if len(usage.queries) != 0 {
		t.Fatalf("summary forecasts read usage history: %+v", usage.queries)
	}
	resp := s.Forecast(t.Context(), cfg, auths, ForecastOptions{Provider: "Claude"})
	if want := now.Add(-defaultRetentionDays * 24 * time.Hour); len(usage.queries) != 1 || !usage.queries[0].Since.Equal(want) {
		t.Fatalf("usage queries = %+v, want one since %s", usage.queries, want)
	}
	pool := resp.Pools[0]
	if pool.Class != "claude:7d" || pool.AccountsTotal != 2 || pool.AccountsWithData != 2 || !pool.Burn.Sufficient {
		t.Fatalf("pool = %+v", pool)
	}
	if pool.Accounts[0].Name != "a.json" {
		t.Fatalf("account name = %q", pool.Accounts[0].Name)
	}
	if pool.Attribution == nil || len(pool.Attribution.Clients) != 1 || pool.Attribution.Clients[0].KeyMasked != "sk-c...1234" {
		t.Fatalf("attribution = %+v", pool.Attribution)
	}
	if other := s.Forecast(t.Context(), cfg, auths, ForecastOptions{Provider: "codex"}); len(other.Pools) != 0 {
		t.Fatalf("provider filter leaked pools: %+v", other.Pools)
	}

	s.flush()
	raw, err := os.ReadFile(filepath.Join(dir, historyFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "sk-client-secret") {
		t.Fatal("raw client key must never be persisted")
	}
	reloaded := testService(&now)
	reloaded.refreshEnabled(cfg)
	if series, _, _ := reloaded.hist.snapshot("claude"); len(series) != 2 {
		t.Fatalf("reloaded series = %d, want 2", len(series))
	}

	cfg.Home.Enabled = true
	if resp := s.Forecast(t.Context(), cfg, auths, ForecastOptions{}); resp.Enabled || resp.Reason != "home_mode" {
		t.Fatalf("home mode response = %+v", resp)
	}
}

func TestForecastAttributionWithoutUsageHistory(t *testing.T) {
	tests := []struct {
		name   string
		reason usagehistory.Reason
		want   string
	}{
		{name: "disabled", reason: usagehistory.ReasonDisabled, want: "usage_history_disabled"},
		{name: "unavailable", reason: usagehistory.ReasonUnavailable, want: "usage_history_unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.AuthDir = t.TempDir()
			cfg.QuotaForecast.Enabled = true
			now := testNow
			s := testService(&now)
			useFakeUsage(t, &fakeUsage{result: usagehistory.HourlyResult{Reason: tt.reason}})
			reset := testNow.Add(72 * time.Hour)
			var auths []*coreauth.Auth
			for i := 0; i < 3; i++ {
				auths = []*coreauth.Auth{claudeAuth("a", 0.2+0.05*float64(i), now, reset)}
				s.Forecast(t.Context(), cfg, auths, ForecastOptions{})
				now = now.Add(time.Hour)
			}
			resp := s.Forecast(t.Context(), cfg, auths, ForecastOptions{Provider: "claude"})
			if len(resp.Pools) != 1 {
				t.Fatalf("pools = %+v", resp.Pools)
			}
			if got := resp.Pools[0].Attribution; got == nil || !reflect.DeepEqual(*got, Attribution{Method: "none", Reason: tt.want}) {
				t.Fatalf("attribution = %+v, want method none, reason %s", got, tt.want)
			}
		})
	}
}
