package requestlog

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/requestlog/prompt"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

const testKey = "sk-test-123456789"

func claudeRecord(session, model, auth string, at time.Time, uncached, read, write int64) coreusage.Record {
	d := coreusage.Detail{InputTokens: uncached, OutputTokens: 20, ReasoningTokens: 4, CacheReadTokens: read, CacheCreationTokens: write, CachedTokens: read}
	if d.CachedTokens == 0 {
		d.CachedTokens = write
	}
	d.TokenBreakdown = coreusage.NewIndependentTokenBreakdown(uncached, read, write, 16, 4, 0)
	return coreusage.Record{
		RequestID:   uuid.NewString(),
		TraceID:     "trace-" + session,
		Provider:    "claude",
		Model:       model,
		APIKey:      testKey,
		SessionID:   session,
		AuthID:      auth,
		Stream:      true,
		RequestedAt: at,
		Latency:     2 * time.Second,
		TTFT:        500 * time.Millisecond,
		Detail:      d,
	}
}

func recordCtx(p *prompt.Print) context.Context {
	ctx := internallogging.WithClientRequestMetadata(context.Background(), internallogging.ClientRequestMetadata{UserAgent: "claude-cli/2.1.3 (external, cli)"})
	return prompt.WithPrint(ctx, p)
}

func testConfig(t *testing.T, enabled bool) *config.Config {
	t.Helper()
	cfg := &config.Config{}
	cfg.AuthDir = t.TempDir()
	cfg.APIKeys = []string{testKey}
	cfg.RequestHistory.Enabled = enabled
	return cfg
}

func testService(cfg *config.Config) (*Service, *coreauth.Manager) {
	manager := coreauth.NewManager(nil, nil, nil)
	manager.SetConfig(cfg)
	s := newService()
	s.now = func() time.Time { return t0.Add(10 * time.Minute) }
	return s, manager
}

func TestServiceDisabledByDefault(t *testing.T) {
	defer prompt.SetCaptureEnabled(false)
	s, manager := testService(testConfig(t, false))
	page := s.Query(Filter{})
	if page.Enabled || page.Reason != "disabled" || page.Entries == nil || page.Keys == nil {
		t.Fatalf("before start = %+v", page)
	}
	s.Start(context.Background(), manager)
	defer s.Stop(context.Background())
	if prompt.CaptureEnabled() {
		t.Fatal("capture gate must stay closed while disabled")
	}
	s.HandleUsage(recordCtx(nil), claudeRecord("s", "opus", "a", t0, 10, 0, 5000))
	if _, newest, _ := s.ring.bounds(); newest != 0 {
		t.Fatal("disabled service must not record")
	}
	if sp := s.Sessions(SessionFilter{}); sp.Enabled || sp.Reason != "disabled" || sp.Sessions == nil {
		t.Fatalf("sessions while disabled = %+v", sp)
	}
}

func TestServiceRefusesHomeMode(t *testing.T) {
	defer prompt.SetCaptureEnabled(false)
	cfg := testConfig(t, true)
	cfg.Home.Enabled = true
	s, manager := testService(cfg)
	s.Start(context.Background(), manager)
	defer s.Stop(context.Background())
	if page := s.Query(Filter{}); page.Enabled || page.Reason != "home_mode" {
		t.Fatalf("home mode = %+v", page)
	}
	if prompt.CaptureEnabled() {
		t.Fatal("capture gate must stay closed in home mode")
	}
}

func TestServiceRecordsAndQueries(t *testing.T) {
	defer prompt.SetCaptureEnabled(false)
	cfg := testConfig(t, true)
	s, manager := testService(cfg)
	s.Start(context.Background(), manager)
	defer s.Stop(context.Background())
	if !prompt.CaptureEnabled() {
		t.Fatal("capture gate must open when enabled")
	}

	base := claudePrintOf(claudeOpts{messages: []string{"one"}})
	moreTools := claudePrintOf(claudeOpts{tools: []string{testToolA, testToolB}, messages: []string{"one"}})
	s.HandleUsage(recordCtx(base), claudeRecord("A", "opus", "a", t0, 10, 0, 5000))
	s.HandleUsage(recordCtx(base), claudeRecord("A", "opus", "a", t0.Add(30*time.Second), 10, 5000, 0))
	s.HandleUsage(recordCtx(moreTools), claudeRecord("A", "opus", "a", t0.Add(60*time.Second), 5010, 0, 0))
	failed := claudeRecord("A", "opus", "a", t0.Add(90*time.Second), 0, 0, 0)
	failed.Failed = true
	failed.Fail = coreusage.Failure{StatusCode: 529, Body: "overloaded"}
	failed.Stream = false
	failed.Detail = coreusage.Detail{}
	s.HandleUsage(recordCtx(nil), failed)
	s.HandleUsage(recordCtx(nil), claudeRecord("B", "haiku", "b", t0.Add(120*time.Second), 500, 0, 0))
	s.HandleUsage(recordCtx(base), claudeRecord("", "opus", "a", t0.Add(150*time.Second), 10, 0, 5000))

	page := s.Query(Filter{})
	if !page.Enabled || page.Epoch == "" || page.Cursor != 6 || page.Oldest != 1 || page.More || len(page.Entries) != 6 {
		t.Fatalf("page = %+v", page)
	}
	if page.Keys[usagehistory.HashClientKey(testKey)] != util.HideAPIKey(testKey) {
		t.Fatalf("keys = %+v", page.Keys)
	}
	first := page.Entries[5]
	if first.Seq != 1 || first.TraceID != "trace-A" || !first.At.Equal(t0) || first.SessionID != "A" || first.KeyHash != usagehistory.HashClientKey(testKey) ||
		first.AuthID != "a" || first.Provider != "claude" || first.Model != "opus" || first.Agent != "claude-cli" || !first.Stream ||
		first.Status != 200 || first.Failed || first.LatencyMs != 2000 || first.TTFTMs != 500 {
		t.Fatalf("first entry = %+v", first)
	}
	if first.Tokens != (Tokens{Input: 10, CacheRead: 0, CacheWrite: 5000, Output: 20, Reasoning: 4, Prompt: 5010}) {
		t.Fatalf("first tokens = %+v", first.Tokens)
	}
	if first.Cache.Verdict != "cold" || first.Cache.Cause != "" {
		t.Fatalf("first cache = %+v", first.Cache)
	}
	if _, err := uuid.Parse(first.RequestID); err != nil {
		t.Fatalf("request id %q: %v", first.RequestID, err)
	}
	if hit := page.Entries[4].Cache; hit.Verdict != "hit" || hit.Expected != 5000 || hit.GapSec != 29 || hit.TTLSec != 300 || hit.Lost != 0 {
		t.Fatalf("hit cache = %+v", hit)
	}
	miss := page.Entries[3].Cache
	if miss.Verdict != "miss" || miss.Cause != "tools_changed" || miss.Confidence != "high" || len(miss.Causes) != 1 || miss.Causes[0] != "tools_changed" ||
		miss.Segment == nil || *miss.Segment != (SegmentRef{Kind: "tools"}) || miss.Tools == nil || *miss.Tools != (ToolsDelta{From: 1, To: 2}) ||
		miss.Expected != 5000 || miss.Lost != 5000 {
		t.Fatalf("miss cache = %+v", miss)
	}
	if f := page.Entries[2]; !f.Failed || f.Status != 529 || f.Error != "overloaded" || f.Stream || f.Cache.Verdict != "na" || f.Cache.Reason != "failed" {
		t.Fatalf("failed entry = %+v", f)
	}
	if na := page.Entries[1].Cache; na.Verdict != "na" || na.Reason != "unsupported" {
		t.Fatalf("unsupported cache = %+v", na)
	}
	if na := page.Entries[0].Cache; na.Verdict != "na" || na.Reason != "no_session" {
		t.Fatalf("no session cache = %+v", na)
	}

	count := func(f Filter) int { return len(s.Query(f).Entries) }
	cases := []struct {
		name string
		f    Filter
		want int
	}{
		{"session", Filter{Session: "A"}, 4},
		{"key", Filter{KeyHash: usagehistory.HashClientKey(testKey)}, 6},
		{"other key", Filter{KeyHash: "nope"}, 0},
		{"auth", Filter{AuthID: "b"}, 1},
		{"model", Filter{Model: "haiku"}, 1},
		{"status ok", Filter{Status: "ok"}, 5},
		{"status failed", Filter{Status: "failed"}, 1},
		{"status code", Filter{Status: "529"}, 1},
		{"verdict csv", Filter{Verdict: "miss,cold"}, 2},
		{"verdict na", Filter{Verdict: "na"}, 3},
		{"from", Filter{From: t0.Add(60 * time.Second)}, 4},
		{"to", Filter{To: t0.Add(60 * time.Second)}, 2},
		{"since", Filter{Since: 4}, 2},
		{"before", Filter{Before: 3}, 2},
	}
	for _, tc := range cases {
		if got := count(tc.f); got != tc.want {
			t.Errorf("%s: %d entries, want %d", tc.name, got, tc.want)
		}
	}
	limited := s.Query(Filter{Limit: 2})
	if len(limited.Entries) != 2 || !limited.More || limited.Entries[0].Seq != 6 {
		t.Fatalf("limit = %+v", limited)
	}
	if asc := s.Query(Filter{Asc: true, Session: "A"}); asc.Entries[0].Seq != 1 || asc.Entries[3].Seq != 4 {
		t.Fatalf("asc = %+v", asc.Entries)
	}

	sp := s.Sessions(SessionFilter{})
	if !sp.Enabled || sp.Epoch != page.Epoch || sp.Cursor != 6 || len(sp.Sessions) != 2 || sp.Sessions[0].SessionID != "B" || sp.Sessions[1].SessionID != "A" {
		t.Fatalf("sessions = %+v", sp)
	}
	a := sp.Sessions[1]
	if a.KeyHash != usagehistory.HashClientKey(testKey) || a.Agent != "claude-cli" || !a.FirstAt.Equal(t0) || !a.LastAt.Equal(t0.Add(90*time.Second)) ||
		a.Requests != 4 || a.Failed != 1 || len(a.Models) != 1 || a.Models[0] != "opus" || len(a.AuthIDs) != 1 || a.AuthIDs[0] != "a" {
		t.Fatalf("session A = %+v", a)
	}
	if a.Verdicts["cold"] != 1 || a.Verdicts["hit"] != 1 || a.Verdicts["miss"] != 1 || a.Verdicts["na"] != 1 {
		t.Fatalf("session A verdicts = %+v", a.Verdicts)
	}
	if a.Tokens != (SessionTokens{Input: 5030, CacheRead: 5000, CacheWrite: 5000, Output: 60, Reasoning: 12, Prompt: 15030}) {
		t.Fatalf("session A tokens = %+v", a.Tokens)
	}
	// hit_ratio = cache_read / prompt over judged (hit, partial, miss) entries: 5000 / (5010 + 5010).
	if a.HitRatio < 0.498 || a.HitRatio > 0.5 || a.LostTokens != 5000 {
		t.Fatalf("session A ratio = %v lost = %d", a.HitRatio, a.LostTokens)
	}
	if a.Causes["tools_changed"] != 1 || a.TopCause != "tools_changed" || a.LastMiss == nil || a.LastMiss.Seq != 3 || a.LastMiss.Cause != "tools_changed" || a.LastMiss.Segment == nil || a.LastMiss.Segment.Kind != "tools" {
		t.Fatalf("session A causes = %+v top=%q last=%+v", a.Causes, a.TopCause, a.LastMiss)
	}
	b := sp.Sessions[0]
	if b.Requests != 1 || b.HitRatio != 0 || b.LastMiss != nil || b.TopCause != "" || len(b.Causes) != 0 {
		t.Fatalf("session B = %+v", b)
	}
	if got := s.Sessions(SessionFilter{Model: "haiku"}); len(got.Sessions) != 1 || got.Sessions[0].SessionID != "B" {
		t.Fatalf("model filter = %+v", got.Sessions)
	}
	if got := s.Sessions(SessionFilter{AuthID: "a"}); len(got.Sessions) != 1 || got.Sessions[0].SessionID != "A" {
		t.Fatalf("auth filter = %+v", got.Sessions)
	}
	if got := s.Sessions(SessionFilter{KeyHash: "nope"}); len(got.Sessions) != 0 {
		t.Fatalf("key filter = %+v", got.Sessions)
	}
	if got := s.Sessions(SessionFilter{From: t0.Add(100 * time.Second)}); len(got.Sessions) != 1 || got.Sessions[0].SessionID != "B" {
		t.Fatalf("from filter = %+v", got.Sessions)
	}
	if got := s.Sessions(SessionFilter{Limit: 1}); len(got.Sessions) != 1 || got.Sessions[0].SessionID != "B" {
		t.Fatalf("limit = %+v", got.Sessions)
	}
}

func TestServiceSnapshotSurvivesRestart(t *testing.T) {
	defer prompt.SetCaptureEnabled(false)
	cfg := testConfig(t, true)
	s, manager := testService(cfg)
	s.Start(context.Background(), manager)
	p := claudePrintOf(claudeOpts{messages: []string{"one"}})
	s.HandleUsage(recordCtx(p), claudeRecord("A", "opus", "a", t0.Add(9*time.Minute), 10, 0, 5000))
	epoch := s.Query(Filter{}).Epoch
	s.Stop(context.Background())
	if _, err := os.Stat(filepath.Join(cfg.AuthDir, snapshotFileName)); err != nil {
		t.Fatalf("snapshot not written: %v", err)
	}

	again, _ := testService(cfg)
	again.Start(context.Background(), manager)
	defer again.Stop(context.Background())
	page := again.Query(Filter{})
	if page.Epoch != epoch || len(page.Entries) != 1 || page.Cursor != 1 {
		t.Fatalf("restored page = %+v", page)
	}
	again.HandleUsage(recordCtx(p), claudeRecord("A", "opus", "a", t0.Add(9*time.Minute+30*time.Second), 10, 5000, 0))
	page = again.Query(Filter{})
	if len(page.Entries) != 2 || page.Entries[0].Seq != 2 || page.Entries[0].Cache.Verdict != "hit" {
		t.Fatalf("baseline must survive the restart: %+v", page.Entries[0].Cache)
	}
}

func TestServiceHotDisableKeepsRows(t *testing.T) {
	defer prompt.SetCaptureEnabled(false)
	cfg := testConfig(t, true)
	s, manager := testService(cfg)
	s.Start(context.Background(), manager)
	defer s.Stop(context.Background())
	s.HandleUsage(recordCtx(nil), claudeRecord("A", "opus", "a", t0, 10, 0, 5000))
	cfg.RequestHistory.Enabled = false
	manager.SetConfig(cfg)
	if page := s.Query(Filter{}); page.Enabled || prompt.CaptureEnabled() {
		t.Fatalf("hot disable must take effect on the next query: %+v", page)
	}
	s.HandleUsage(recordCtx(nil), claudeRecord("A", "opus", "a", t0.Add(time.Second), 10, 0, 5000))
	cfg.RequestHistory.Enabled = true
	manager.SetConfig(cfg)
	if page := s.Query(Filter{}); !page.Enabled || len(page.Entries) != 1 {
		t.Fatalf("re-enable must show the rows kept in memory: %+v", page)
	}
}

func TestServiceConfigClamps(t *testing.T) {
	cfg := &config.Config{}
	if maxEntries(cfg) != defaultMaxEntries || retentionDays(cfg) != defaultRetentionDays || maxSessions(cfg) != defaultMaxSessions {
		t.Fatal("zero config must use the defaults")
	}
	cfg.RequestHistory = config.RequestHistoryConfig{MaxEntries: 1 << 30, RetentionDays: 400, MaxSessions: 1 << 30}
	if maxEntries(cfg) != maxMaxEntries || retentionDays(cfg) != maxRetentionDays || maxSessions(cfg) != maxMaxSessions {
		t.Fatal("oversized config must clamp to the maxima")
	}
	cfg.RequestHistory = config.RequestHistoryConfig{MaxEntries: 7, RetentionDays: 2, MaxSessions: 3}
	if maxEntries(cfg) != 7 || retentionDays(cfg) != 2 || maxSessions(cfg) != 3 {
		t.Fatal("in-range config must pass through")
	}
	if maxEntries(nil) != defaultMaxEntries {
		t.Fatal("nil config must use the defaults")
	}
}

// Publish order (seq) differs from request start order (at) whenever a long
// request finishes after a later short one. Session bounds and order follow at.
func TestSessionsOrderAndBoundsFollowRequestTime(t *testing.T) {
	defer prompt.SetCaptureEnabled(false)
	s, manager := testService(testConfig(t, true))
	s.Start(context.Background(), manager)
	defer s.Stop(context.Background())
	p := claudePrintOf(claudeOpts{messages: []string{"one"}})
	s.HandleUsage(recordCtx(p), claudeRecord("A", "opus", "a", t0.Add(100*time.Second), 10, 0, 5000))
	s.HandleUsage(recordCtx(p), claudeRecord("B", "opus", "a", t0.Add(50*time.Second), 10, 0, 5000))
	s.HandleUsage(recordCtx(p), claudeRecord("A", "opus", "a", t0.Add(20*time.Second), 10, 0, 5000))
	s.HandleUsage(recordCtx(p), claudeRecord("C", "opus", "a", t0.Add(30*time.Second), 10, 0, 5000))

	page := s.Sessions(SessionFilter{})
	var got []string
	for _, sess := range page.Sessions {
		got = append(got, sess.SessionID)
	}
	if len(got) != 3 || got[0] != "A" || got[1] != "B" || got[2] != "C" {
		t.Fatalf("order = %v, want [A B C] by last request time", got)
	}
	a := page.Sessions[0]
	if !a.LastAt.Equal(t0.Add(100*time.Second)) || !a.FirstAt.Equal(t0.Add(20*time.Second)) {
		t.Fatalf("A bounds = %v .. %v", a.FirstAt, a.LastAt)
	}
	if limited := s.Sessions(SessionFilter{Limit: 1}); len(limited.Sessions) != 1 || limited.Sessions[0].SessionID != "A" {
		t.Fatalf("limit must keep the most recent session, got %+v", limited.Sessions)
	}
}
