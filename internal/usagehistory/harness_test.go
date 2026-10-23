package usagehistory

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory/prices"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

var harnessStart = time.Date(2026, time.October, 22, 15, 30, 0, 0, time.UTC)

// harness runs a Service with a hand-driven clock and ticker, a temp auth dir,
// and a price fetch that never touches the network.
type harness struct {
	t       *testing.T
	s       *Service
	manager *coreauth.Manager
	cfg     config.Config
	clock   atomic.Int64
	tick    chan time.Time
	fetches atomic.Int32
	dir     string
}

func newHarness(t *testing.T, mutate func(*config.Config)) *harness {
	t.Helper()
	h := &harness{t: t, tick: make(chan time.Time), dir: t.TempDir()}
	h.clock.Store(harnessStart.UnixNano())
	h.cfg = config.Config{AuthDir: h.dir}
	h.cfg.UsageHistory.Enabled = true
	h.cfg.UsageHistory.Prices.DisableRemote = true
	if mutate != nil {
		mutate(&h.cfg)
	}
	h.s = h.newService(func(context.Context, string, string, time.Time) (prices.FetchResult, error) {
		h.fetches.Add(1)
		return prices.FetchResult{}, context.Canceled
	})
	h.manager = coreauth.NewManager(nil, nil, nil)
	h.manager.SetConfig(h.copyConfig())
	h.s.Start(context.Background(), h.manager)
	t.Cleanup(func() { h.s.Stop(context.Background()) })
	return h
}

func (h *harness) newService(fetch fetchFunc) *Service {
	s := newService()
	s.now = h.now
	s.newTicker = func() (<-chan time.Time, func()) { return h.tick, func() {} }
	s.fetch = fetch
	return s
}

func (h *harness) copyConfig() *config.Config {
	c := h.cfg
	return &c
}

func (h *harness) now() time.Time { return time.Unix(0, h.clock.Load()).UTC() }

func (h *harness) advance(d time.Duration) { h.clock.Add(int64(d)) }

func (h *harness) setConfig(mutate func(*config.Config)) {
	mutate(&h.cfg)
	h.manager.SetConfig(h.copyConfig())
}

// tickNow delivers one tick and returns after the loop has run the step for it:
// the next sync is only served once the tick's step is done.
func (h *harness) tickNow() {
	h.tick <- h.now()
	h.s.sync(context.Background())
}

func (h *harness) record(rec coreusage.Record) {
	if rec.RequestedAt.IsZero() {
		rec.RequestedAt = h.now()
	}
	h.s.HandleUsage(context.Background(), rec)
}

func (h *harness) report(q Query) Report {
	h.t.Helper()
	if q.Loc == nil {
		q.Loc = time.UTC
	}
	rep, err := h.s.Report(context.Background(), q)
	if err != nil {
		h.t.Fatalf("Report: %v", err)
	}
	return rep
}

func (h *harness) dbPath() string { return filepath.Join(h.dir, dbFileName) }

// readRows opens a stopped service's file directly and returns every row.
func readRows(t *testing.T, path string) []HourlyRow {
	t.Helper()
	st, err := openStore(context.Background(), path)
	if err != nil {
		t.Fatalf("openStore(%s): %v", path, err)
	}
	defer func() {
		if errClose := st.close(); errClose != nil {
			t.Errorf("close: %v", errClose)
		}
	}()
	rows, err := st.scanHourly(context.Background(), 0, nil)
	if err != nil {
		t.Fatalf("scanHourly: %v", err)
	}
	return rows
}

func claudeRecord(model string, out int64) coreusage.Record {
	return coreusage.Record{
		Provider: "claude", AuthID: "claude-a.json", Model: model, APIKey: "sk-test",
		Detail: coreusage.Detail{InputTokens: 10, OutputTokens: out},
	}
}
