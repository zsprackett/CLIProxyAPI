package quotaforecast

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

func claudeAuth(id string, utilization float64, observed, reset time.Time) *coreauth.Auth {
	return &coreauth.Auth{
		ID:       id,
		Provider: "claude",
		FileName: "/auths/" + id + ".json",
		Quota: coreauth.QuotaState{ObservedAt: observed, Signals: map[string]string{
			http.CanonicalHeaderKey("Anthropic-Ratelimit-Unified-7d-Utilization"): strconv.FormatFloat(utilization, 'f', -1, 64),
			http.CanonicalHeaderKey("Anthropic-Ratelimit-Unified-7d-Reset"):       strconv.FormatInt(reset.Unix(), 10),
		}},
	}
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

	if resp := s.Forecast(&config.Config{}, nil, ForecastOptions{}); resp.Enabled || resp.Reason != "disabled" {
		t.Fatalf("disabled response = %+v", resp)
	}

	for i := 0; i <= 12; i++ {
		used := 0.26 + 0.02*float64(i)
		auths := []*coreauth.Auth{claudeAuth("a", used, now, reset), claudeAuth("b", used, now, reset)}
		ctx := context.Background()
		s.HandleUsage(ctx, coreusage.Record{Provider: "claude", AuthID: "a", Model: "claude-sonnet-5-5", APIKey: cfg.APIKeys[0], RequestedAt: now, Detail: coreusage.Detail{TotalTokens: 1000}})
		s.Forecast(cfg, auths, ForecastOptions{})
		now = now.Add(time.Hour)
	}
	now = now.Add(-time.Hour)
	auths := []*coreauth.Auth{claudeAuth("a", 0.5, now, reset), claudeAuth("b", 0.5, now, reset)}

	summary := s.Forecast(cfg, auths, ForecastOptions{})
	if !summary.Enabled || len(summary.Pools) != 1 || summary.Pools[0].Chart != nil {
		t.Fatalf("summary = %+v", summary)
	}
	resp := s.Forecast(cfg, auths, ForecastOptions{Provider: "Claude"})
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
	if other := s.Forecast(cfg, auths, ForecastOptions{Provider: "codex"}); len(other.Pools) != 0 {
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
	if series, _, _, _ := reloaded.hist.snapshot("claude"); len(series) != 2 {
		t.Fatalf("reloaded series = %d, want 2", len(series))
	}

	cfg.Home.Enabled = true
	if resp := s.Forecast(cfg, auths, ForecastOptions{}); resp.Enabled || resp.Reason != "home_mode" {
		t.Fatalf("home mode response = %+v", resp)
	}
}

func TestHandleUsageIgnoresDisabledAndUnsupported(t *testing.T) {
	now := testNow
	s := testService(&now)
	record := coreusage.Record{Provider: "claude", AuthID: "a", Model: "m", Detail: coreusage.Detail{TotalTokens: 10}}
	s.HandleUsage(context.Background(), record)
	s.enabled.Store(true)
	record.Provider = "gemini"
	s.HandleUsage(context.Background(), record)
	if _, usage, _, _ := s.hist.snapshot(""); len(usage) != 0 {
		t.Fatalf("usage recorded while disabled or unsupported: %+v", usage)
	}
}

func TestUsageHelpers(t *testing.T) {
	if got := agentFamily("claude-cli/2.1.3 (external, cli)"); got != "claude-cli" {
		t.Fatalf("agent = %q", got)
	}
	if got := agentFamily(""); got != "-" {
		t.Fatalf("empty agent = %q", got)
	}
	if HashClientKey("") != "-" || len(HashClientKey("k")) != 12 || HashClientKey("k") == HashClientKey("j") {
		t.Fatal("client key hashing")
	}
	if got := uncachedTokens(coreusage.Detail{TotalTokens: 100, CachedTokens: 30, CacheReadTokens: 40}); got != 60 {
		t.Fatalf("uncached = %d, want 60", got)
	}
}
