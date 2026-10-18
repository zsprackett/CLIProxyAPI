// Package requestlog records every upstream attempt in a bounded in-memory log
// with a per-entry prompt-cache verdict, persists it as a gzip snapshot under the
// auth dir, and serves it to the management API.
//
// Public surface (everything a caller can touch):
//
//	Default() *Service
//	(*Service).HandleUsage(ctx, usage.Record)        // usage.Plugin
//	(*Service).Start(ctx, *coreauth.Manager)
//	(*Service).Stop(ctx)
//	(*Service).Query(Filter) Page
//	(*Service).Sessions(SessionFilter) SessionPage
//	Filter, Page, Entry, Tokens, Cache, SegmentRef, ToolsDelta,
//	SessionFilter, SessionPage, Session, SessionTokens, MissRef   (api.go, JSON views)
//
// Hidden: the row layout, interning, the ring, seq math, the session tracker and
// its lanes, Prints, the verdict thresholds, the snapshot format, config clamping.
//
// Module map (state owner -> what it exposes inside the package):
//
//	ring     (store.go)    owns rows, seq, epoch, intern table   -> append, scan, prune, resize, copyForSave, load
//	tracker  (sessions.go) owns lanes (the only Prints)          -> observe (judge + update), export, restore
//	judge    (verdict.go)  pure, no state                        -> Verdict from (observation, *session)
//	Service  (service.go)  owns lifecycle, path, enabled flag    -> the public surface above
//	prompt   (prompt/)     pure, leaf package                    -> Print, Of, Diverge, WithPrint/FromContext
package requestlog

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

const (
	tickInterval = time.Minute
	flushEvery   = 5 // ticks
)

// Service is the request-log plugin and query service.
type Service struct {
	ring    *ring
	tracker *tracker
	enabled atomic.Bool
	now     func() time.Time

	// mu guards lifecycle and the snapshot path only; never held during record
	// handling or queries.
	mu      sync.Mutex
	manager *coreauth.Manager // for RuntimeConfig in Query/Sessions (hot reload without waiting a tick)
	cancel  context.CancelFunc
	done    chan struct{}
	path    string
	loaded  string
	reason  string // why disabled: "disabled" | "home_mode" | "unavailable"
}

var defaultService = newService()

// Default returns the process-wide request log.
func Default() *Service { return defaultService }

func newService() *Service {
	return &Service{
		ring:    newRing(defaultMaxEntries),
		tracker: newTracker(defaultMaxSessions),
		now:     time.Now,
		reason:  "disabled",
	}
}

// Start records the manager, runs one refresh, and starts the tick loop. No-op
// when already running. Not called in home mode (service_lifecycle.go only
// starts it in the non-home branch), and refreshEnabled also rejects cfg.Home.Enabled.
func (s *Service) Start(ctx context.Context, manager *coreauth.Manager) { panic("not implemented") }

// Stop ends the tick loop, which flushes the snapshot before returning.
// Waits until done or ctx expires.
func (s *Service) Stop(ctx context.Context) { panic("not implemented") }

// run is the tick loop.
//
//	for each tick (and once immediately):
//	    cfg := manager.RuntimeConfig()
//	    if !s.refreshEnabled(cfg): continue
//	    ring.prune(now - retentionDays)
//	    ring.resize(maxEntries(cfg)); tracker.resize(maxSessions(cfg))
//	    if tick % flushEvery == 0: s.flush()
//	on ctx.Done: s.flush(); return
func (s *Service) run(ctx context.Context, done chan struct{}) { panic("not implemented") }

// refreshEnabled reads cfg.RequestHistory, sets s.enabled and
// prompt.SetCaptureEnabled, resolves <auth-dir>/.request-history.gz, and loads
// the snapshot the first time a path is seen (ring.load then tracker.restore).
//
//	enabled := cfg != nil && cfg.RequestHistory.Enabled && !cfg.Home.Enabled
//	disabling clears capture immediately but keeps rows in memory, so re-enabling
//	shows history again; rows are flushed before the flag drops.
func (s *Service) refreshEnabled(cfg *config.Config) bool { panic("not implemented") }

// flush writes the snapshot if the ring is dirty: copyForSave under RLock,
// tracker.export, encode + gzip + atomic rename outside both locks.
func (s *Service) flush() { panic("not implemented") }

// Query returns entries matching f.
//
//	cfg := s.runtimeConfig(); if !s.refreshEnabled(cfg): return Page{Enabled:false, Reason: s.reason}
//	limit := clamp(f.Limit, 200, 1, 2000)
//	lo := max(f.Since+1, oldest); hi := f.Before-1 or newest
//	ring.scan(lo, hi, f.Asc, func(r) { if match(r, f) { append view(r) }; stop at limit+1 })
//	page.More = matched > limit
//	page.Keys = masked labels for key hashes present (cfg.APIKeys via HashClientKey + util.HideAPIKey)
func (s *Service) Query(f Filter) Page { panic("not implemented") }

// Sessions aggregates entries per session.
//
//	one newest-first scan over the ring under RLock (<= max-entries rows):
//	    skip rows with empty session or not matching the time window
//	    agg[session] += row  (counts, token sums, verdict and cause histograms, models, auths)
//	drop sessions that fail the KeyHash/AuthID/Model filter (match on any row)
//	sort by last_at desc, cut to limit, compute hit_ratio and top_cause
//
// Computed on demand rather than kept incrementally: one owner (the ring), no
// second structure to keep consistent with pruning, and the scan is ~1-3 ms at
// the default size.
func (s *Service) Sessions(f SessionFilter) SessionPage { panic("not implemented") }

// runtimeConfig returns manager.RuntimeConfig() or nil before Start.
func (s *Service) runtimeConfig() *config.Config { panic("not implemented") }

// clamping helpers: zero -> default, then bound to [1, max].
func maxEntries(cfg *config.Config) int    { panic("not implemented") }
func retentionDays(cfg *config.Config) int { panic("not implemented") }
func maxSessions(cfg *config.Config) int   { panic("not implemented") }
