package usagehistory

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory/prices"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestDecide(t *testing.T) {
	now := harnessStart
	on := func(p string) want { return want{on: true, path: p} }
	off := want{reason: ReasonDisabled}
	cases := []struct {
		name string
		cur  status
		w    want
		want action
	}{
		{"idle stays idle when on", status{phase: phaseIdle}, on("p"), actNone},
		{"idle stays idle when off", status{phase: phaseIdle}, off, actNone},
		{"off stays off", status{phase: phaseOff}, off, actNone},
		{"off opens", status{phase: phaseOff}, on("p"), actOpen},
		{"open same path", status{phase: phaseOpen, path: "p"}, on("p"), actNone},
		{"open moves", status{phase: phaseOpen, path: "p"}, on("q"), actMove},
		{"open closes", status{phase: phaseOpen, path: "p"}, off, actFlushClose},
		{"open closes for home mode", status{phase: phaseOpen, path: "p"}, want{reason: ReasonHomeMode}, actFlushClose},
		{"broken retries after backoff", status{phase: phaseBroken, path: "p", lastOpen: now.Add(-reopenBackoff)}, on("p"), actRetry},
		{"broken waits in backoff", status{phase: phaseBroken, path: "p", lastOpen: now.Add(-reopenBackoff + time.Second)}, on("p"), actNone},
		{"broken opens a new path at once", status{phase: phaseBroken, path: "p", lastOpen: now}, on("q"), actOpen},
		{"broken discards", status{phase: phaseBroken, path: "p", lastOpen: now}, off, actDiscard},
	}
	for _, c := range cases {
		if got := decide(c.cur, c.w, now); got != c.want {
			t.Errorf("%s: decide = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestWantOf(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name      string
		cfg       *config.Config
		on        bool
		reason    Reason
		retention int
	}{
		{"nil config", nil, false, ReasonDisabled, defaultRetentionDays},
		{"disabled", &config.Config{AuthDir: dir}, false, ReasonDisabled, defaultRetentionDays},
		{"home mode", &config.Config{AuthDir: dir, Home: config.HomeConfig{Enabled: true}, UsageHistory: config.UsageHistoryConfig{Enabled: true}}, false, ReasonHomeMode, defaultRetentionDays},
		{"on", &config.Config{AuthDir: dir, UsageHistory: config.UsageHistoryConfig{Enabled: true, RetentionDays: 30}}, true, ReasonNone, 30},
		{"retention clamped high", &config.Config{AuthDir: dir, UsageHistory: config.UsageHistoryConfig{Enabled: true, RetentionDays: 99999}}, true, ReasonNone, maxRetentionDays},
		{"retention default for negative", &config.Config{AuthDir: dir, UsageHistory: config.UsageHistoryConfig{Enabled: true, RetentionDays: -4}}, true, ReasonNone, defaultRetentionDays},
	}
	for _, c := range cases {
		w := wantOf(c.cfg)
		if w.on != c.on || w.reason != c.reason || w.retention != c.retention {
			t.Errorf("%s: wantOf = on %v reason %q retention %d", c.name, w.on, w.reason, w.retention)
		}
		if w.on && w.path != filepath.Join(dir, dbFileName) {
			t.Errorf("%s: path = %q", c.name, w.path)
		}
	}
	rate := 3.0
	a := wantOf(&config.Config{UsageHistory: config.UsageHistoryConfig{Prices: config.UsagePricesConfig{
		Models: []config.ModelPriceOverride{{Model: "m", Provider: "p", SameAs: "x", Input: &rate}},
	}}})
	if len(a.prices.overrides) != 1 || a.prices.overrides[0].PerMTok.Input == nil || *a.prices.overrides[0].PerMTok.Input != 3 ||
		a.prices.overrides[0].SameAs != "x" || a.prices.url != prices.DefaultURL || !a.prices.remote {
		t.Fatalf("prices want = %+v", a.prices)
	}
	b := wantOf(&config.Config{})
	if a.prices.fingerprint == b.prices.fingerprint {
		t.Fatal("an override change kept the fingerprint")
	}
}

func TestServiceRecordsAndReportsImmediately(t *testing.T) {
	h := newHarness(t, nil)
	h.record(claudeRecord("claude-opus-5-5", 100))
	rep := h.report(Query{})
	if !rep.Enabled || rep.Totals.Requests != 1 || rep.Totals.Tokens.Output != 100 {
		t.Fatalf("report = enabled %v totals %+v", rep.Enabled, rep.Totals)
	}
	if !rep.AsOf.Equal(harnessStart) {
		t.Fatalf("as_of = %v", rep.AsOf)
	}
	if _, err := os.Stat(h.dbPath()); err != nil {
		t.Fatalf("db file: %v", err)
	}
}

func TestServiceEnableAndDisableByConfig(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.UsageHistory.Enabled = false })
	h.record(claudeRecord("m", 1))
	if rep := h.report(Query{}); rep.Enabled || rep.Reason != ReasonDisabled {
		t.Fatalf("disabled report = %v %q", rep.Enabled, rep.Reason)
	}
	if _, err := os.Stat(h.dbPath()); !os.IsNotExist(err) {
		t.Fatalf("disabled service created the file: %v", err)
	}

	h.setConfig(func(c *config.Config) { c.UsageHistory.Enabled = true })
	h.s.sync(context.Background())
	h.record(claudeRecord("m", 1))
	if rep := h.report(Query{}); !rep.Enabled || rep.Totals.Requests != 1 {
		t.Fatalf("enabled report = %v %+v (the record sent while disabled must not count)", rep.Enabled, rep.Totals)
	}

	h.record(claudeRecord("m", 1))
	h.setConfig(func(c *config.Config) { c.UsageHistory.Enabled = false })
	if rep := h.report(Query{}); rep.Enabled || rep.Reason != ReasonDisabled {
		t.Fatalf("report after disable = %v %q", rep.Enabled, rep.Reason)
	}
	if h.s.withReader(func(*store) {}) {
		t.Fatal("store still open after disable")
	}
	// The record sent before the disable was flushed by it.
	h.s.Stop(context.Background())
	if rows := readRows(t, h.dbPath()); len(rows) != 1 || rows[0].Requests != 2 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestServiceHomeMode(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.Home.Enabled = true })
	if rep := h.report(Query{}); rep.Enabled || rep.Reason != ReasonHomeMode {
		t.Fatalf("report = %v %q", rep.Enabled, rep.Reason)
	}
	if res := h.s.Hourly(context.Background(), HourlyQuery{}); res.Enabled || res.Reason != ReasonHomeMode {
		t.Fatalf("hourly = %+v", res)
	}
}

func TestServiceNotStarted(t *testing.T) {
	s := newService()
	rep, err := s.Report(context.Background(), Query{})
	if err != nil || rep.Enabled || rep.Reason != ReasonDisabled || rep.Buckets == nil || rep.Options.Model == nil || rep.Keys == nil {
		t.Fatalf("report = %+v, %v", rep, err)
	}
	if p := s.Prices(context.Background()); p.Enabled || p.Models == nil {
		t.Fatalf("prices = %+v", p)
	}
}

func TestServiceAuthDirMove(t *testing.T) {
	h := newHarness(t, nil)
	h.record(claudeRecord("first", 1))
	h.report(Query{})
	second := t.TempDir()
	h.setConfig(func(c *config.Config) { c.AuthDir = second })
	// Records drained before the loop sees the change still go to the old file.
	h.s.sync(context.Background())
	h.record(claudeRecord("second", 1))
	if rep := h.report(Query{}); !rep.Enabled || rep.Totals.Requests != 1 {
		t.Fatalf("report after move = %v %+v (a new file starts empty)", rep.Enabled, rep.Totals)
	}
	h.s.Stop(context.Background())
	old := readRows(t, h.dbPath())
	moved := readRows(t, filepath.Join(second, dbFileName))
	if len(old) != 1 || old[0].Model != "first" || len(moved) != 1 || moved[0].Model != "second" {
		t.Fatalf("old file %+v, new file %+v", old, moved)
	}
}

func TestServiceBrokenRetriesAfterBackoff(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, dbFileName)
	if err := os.MkdirAll(blocker, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	h := newHarness(t, func(c *config.Config) { c.AuthDir = dir })
	h.dir = dir
	h.record(claudeRecord("m", 5))
	if rep := h.report(Query{}); rep.Enabled || rep.Reason != ReasonUnavailable {
		t.Fatalf("broken report = %v %q", rep.Enabled, rep.Reason)
	}
	if res := h.s.Hourly(context.Background(), HourlyQuery{}); res.Reason != ReasonUnavailable {
		t.Fatalf("broken hourly = %+v", res)
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatalf("remove blocker: %v", err)
	}
	h.advance(reopenBackoff - time.Second)
	if rep := h.report(Query{}); rep.Enabled {
		t.Fatal("reopened inside the backoff")
	}
	h.advance(time.Second)
	rep := h.report(Query{})
	if !rep.Enabled || rep.Totals.Requests != 1 || rep.Totals.Tokens.Output != 5 {
		t.Fatalf("report after retry = %v %+v (the record taken while broken must be kept)", rep.Enabled, rep.Totals)
	}
}

func TestServiceBrokenThenDisabledDiscards(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, dbFileName), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	h := newHarness(t, func(c *config.Config) { c.AuthDir = dir })
	h.record(claudeRecord("m", 5))
	h.setConfig(func(c *config.Config) { c.UsageHistory.Enabled = false })
	h.s.sync(context.Background())
	if batch, _ := h.s.pend.swap(); len(batch) != 0 {
		t.Fatalf("pending kept %d rows after disable", len(batch))
	}
}

func TestServiceStopFlushesPending(t *testing.T) {
	h := newHarness(t, nil)
	h.record(claudeRecord("m", 7))
	h.s.Stop(context.Background())
	rows := readRows(t, h.dbPath())
	if len(rows) != 1 || rows[0].Tokens.Output != 7 {
		t.Fatalf("rows = %+v", rows)
	}
	h.record(claudeRecord("m", 7))
	if h.s.recording.Load() {
		t.Fatal("still recording after Stop")
	}
}

func TestServiceIgnoresStartContextCancel(t *testing.T) {
	h := &harness{t: t, tick: make(chan time.Time), dir: t.TempDir()}
	h.clock.Store(harnessStart.UnixNano())
	h.cfg = config.Config{AuthDir: h.dir}
	h.cfg.UsageHistory.Enabled = true
	h.cfg.UsageHistory.Prices.DisableRemote = true
	h.s = h.newService(nil)
	h.manager = coreauth.NewManager(nil, nil, nil)
	h.manager.SetConfig(h.copyConfig())
	ctx, cancel := context.WithCancel(context.Background())
	h.s.Start(ctx, h.manager)
	cancel()
	// Run cancels its ctx before Shutdown drains the usage queue.
	h.record(claudeRecord("m", 3))
	h.s.Stop(context.Background())
	if rows := readRows(t, h.dbPath()); len(rows) != 1 || rows[0].Tokens.Output != 3 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestServiceRetentionCleanupOnTick(t *testing.T) {
	h := newHarness(t, nil)
	old := claudeRecord("old", 1)
	old.RequestedAt = h.now().Add(-10 * 24 * time.Hour)
	h.record(old)
	h.record(claudeRecord("new", 1))
	h.tickNow()
	if res := h.s.Hourly(context.Background(), HourlyQuery{}); len(res.Rows) != 2 {
		t.Fatalf("rows before the retention change = %+v", res.Rows)
	}
	h.setConfig(func(c *config.Config) { c.UsageHistory.RetentionDays = 5 })
	h.tickNow()
	res := h.s.Hourly(context.Background(), HourlyQuery{})
	if len(res.Rows) != 1 || res.Rows[0].Model != "new" {
		t.Fatalf("rows after cleanup = %+v", res.Rows)
	}
	if rep := h.report(Query{}); rep.RetentionDays != 5 || !rep.Query.Clamped {
		t.Fatalf("report retention %d clamped %v", rep.RetentionDays, rep.Query.Clamped)
	}
}

func TestServiceHourlyFiltersAndMergesTiers(t *testing.T) {
	h := newHarness(t, nil)
	long := claudeRecord("m", 1)
	long.Detail.CacheReadTokens = LongContextThreshold + 1
	h.record(long)
	h.record(claudeRecord("m", 1))
	codex := claudeRecord("gpt", 1)
	codex.Provider = "codex"
	h.record(codex)
	res := h.s.Hourly(context.Background(), HourlyQuery{Since: h.now(), Providers: []string{"claude"}})
	if !res.Enabled || len(res.Rows) != 1 || res.Rows[0].Requests != 2 || res.Rows[0].Tokens.CacheRead != LongContextThreshold+1 {
		t.Fatalf("hourly = %+v", res)
	}
}

func TestServicePriceFetchIsCachedAcrossRestart(t *testing.T) {
	remote := prices.Builtin()
	remote.Source = prices.SourceRemote
	remote.Generated = remote.Generated.Add(time.Hour) // a fetch is newer than the snapshot
	body, err := remote.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	h := newHarness(t, func(c *config.Config) { c.UsageHistory.Prices.DisableRemote = false })
	fetched := make(chan string, 1)
	h.s.fetch = func(_ context.Context, url, etag string, now time.Time) (prices.FetchResult, error) {
		fetched <- url
		return prices.FetchResult{Catalog: remote, Body: body, ETag: `"v1"`, At: now}, nil
	}
	h.tickNow()
	if url := <-fetched; url != prices.DefaultURL {
		t.Fatalf("fetched %q", url)
	}
	waitForSource(t, h.s, "remote")
	h.s.Stop(context.Background())

	// A restart within the day reads the cached catalog and does not refetch.
	h2 := &harness{t: t, tick: make(chan time.Time), dir: h.dir}
	h2.clock.Store(h.now().Add(time.Hour).UnixNano())
	h2.cfg = h.cfg
	h2.s = h2.newService(func(context.Context, string, string, time.Time) (prices.FetchResult, error) {
		h2.fetches.Add(1)
		return prices.FetchResult{}, context.Canceled
	})
	h2.manager = coreauth.NewManager(nil, nil, nil)
	h2.manager.SetConfig(h2.copyConfig())
	h2.s.Start(context.Background(), h2.manager)
	defer h2.s.Stop(context.Background())
	h2.tickNow()
	p := h2.s.Prices(context.Background())
	if p.Catalog.Source != "remote" || p.Catalog.FetchedAt == nil || !p.Catalog.FetchedAt.Equal(harnessStart) {
		t.Fatalf("catalog after restart = %+v", p.Catalog)
	}
	if n := h2.fetches.Load(); n != 0 {
		t.Fatalf("refetched %d times within the day", n)
	}
}

// waitForSource polls until the loop has applied a finished fetch. The fetch
// result is handed back on a channel, so a few syncs may run before it lands.
func waitForSource(t *testing.T, s *Service, source string) {
	t.Helper()
	for i := 0; i < 10_000; i++ {
		if s.Prices(context.Background()).Catalog.Source == source {
			return
		}
		runtime.Gosched()
	}
	t.Fatalf("catalog source never became %q", source)
}

func TestServicePriceOverridesFollowConfig(t *testing.T) {
	h := newHarness(t, nil)
	h.record(claudeRecord("my-model", 1))
	if p := h.s.Prices(context.Background()); len(p.Models) != 1 || p.Models[0].Priced {
		t.Fatalf("prices before override = %+v", p.Models)
	}
	in, out := 1.0, 2.0
	h.setConfig(func(c *config.Config) {
		c.UsageHistory.Prices.Models = []config.ModelPriceOverride{{Model: "my-model", Input: &in, Output: &out}}
	})
	p := h.s.Prices(context.Background())
	if len(p.Models) != 2 {
		t.Fatalf("models = %+v (history row and the provider-less override)", p.Models)
	}
	for _, m := range p.Models {
		if !m.Priced || m.Source != prices.SourceOverride || m.PerMTok.Input != 1 || m.PerMTok.Output != 2 {
			t.Fatalf("model price = %+v", m)
		}
	}
}
