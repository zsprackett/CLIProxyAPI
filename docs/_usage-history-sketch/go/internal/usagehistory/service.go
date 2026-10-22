package usagehistory

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory/prices"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

const (
	tickInterval         = time.Minute
	cleanupInterval      = 24 * time.Hour
	reopenBackoff        = time.Minute // phaseBroken retries at most this often
	defaultRetentionDays = 365
	maxRetentionDays     = 3650
)

// ---------------------------------------------------------------------------
// Lifecycle as types.
// ---------------------------------------------------------------------------

// phase is the service's lifecycle state. Owned by the run loop; readers see a
// copy through sync().
type phase uint8

const (
	phaseIdle   phase = iota // before Start / after Stop: no DB, not recording
	phaseOff                 // running; config disabled or home mode: no DB, not recording
	phaseOpen                // running; DB open at path: recording, flushing each tick
	phaseBroken              // running; enabled but DB unopenable at path: recording into
	//                          pending (bounded), retrying open every reopenBackoff
)

// want is what the config asks for, computed from *config.Config by wantOf.
type want struct {
	on        bool
	reason    Reason // when !on: disabled | home_mode | unavailable (auth dir unresolvable)
	path      string // when on: <auth-dir>/.usage-history.db
	retention int    // clamped days
	prices    pricesWant
}

// pricesWant is the price part of the config; a fingerprint change rebuilds the table.
type pricesWant struct {
	remote      bool
	url         string
	overrides   []prices.Override
	fingerprint string // stable hash of the three fields above
}

// status is the run loop's owned state.
type status struct {
	phase      phase
	path       string
	reason     Reason
	st         *store
	lastOpen   time.Time // last open attempt (phaseBroken backoff)
	asOf       time.Time // last successful flush
	retention  int
	cleanedAt  time.Time
	cleanedFor int // retention value the last cleanup used; a change forces one
}

// action is what a transition does.
type action uint8

const (
	actNone       action = iota
	actOpen              // recording on; open(path): ok -> phaseOpen, err -> phaseBroken
	actRetry             // phaseBroken and backoff elapsed: same as actOpen
	actFlushClose        // recording off; flush pending to the open DB; close -> phaseOff
	actMove              // flush to old path, close, open new path (auth-dir changed)
	actDiscard           // recording off; drop pending -> phaseOff
)

// decide is the transition table. Pure.
//
//	| from       | want              | action      | to (ok / err)       |
//	|------------|-------------------|-------------|---------------------|
//	| Idle       | (any)             | none        | Idle (Start moves Idle -> Off, then decides) |
//	| Off        | off(r)            | none        | Off(r)              |
//	| Off        | on(p)             | open        | Open(p) / Broken(p) |
//	| Open(p)    | on(p)             | none        | Open(p)             |
//	| Open(p)    | on(q), q != p     | move        | Open(q) / Broken(q) |
//	| Open(p)    | off(r)            | flushClose  | Off(r)              |
//	| Broken(p)  | on(p), backoff ok | retry       | Open(p) / Broken(p) |
//	| Broken(p)  | on(p), in backoff | none        | Broken(p)           |
//	| Broken(p)  | on(q), q != p     | open        | Open(q) / Broken(q) |
//	| Broken(p)  | off(r)            | discard     | Off(r)              |
//	| any running| ctx cancelled     | (run exit)  | flush if Open, close -> Idle |
//
// Recording flag: true exactly in Open and Broken. Set BEFORE open (so no record
// published after enabling is lost) and cleared BEFORE the final flush of a
// flushClose (so nothing lands in pending after it).
func decide(cur status, w want, now time.Time) action { panic("not implemented") }

// wantOf reads the pull-model config. Pure apart from util.ResolveAuthDir.
//
//	cfg == nil || !cfg.UsageHistory.Enabled -> off(disabled)
//	cfg.Home.Enabled                          -> off(home_mode)
//	ResolveAuthDir error                      -> off(unavailable)
//	retention = clamp(cfg.UsageHistory.RetentionDays, default 365, 1..3650)
//	prices    = convert cfg.UsageHistory.Prices (per-MTok overrides -> prices.Override)
func wantOf(cfg *config.Config) want { panic("not implemented") }

// ---------------------------------------------------------------------------
// Service.
// ---------------------------------------------------------------------------

// syncReq asks the run loop to refresh config and flush pending now.
type syncReq struct{ reply chan status }

// priceDone is a finished background fetch, handed back to the loop to persist.
type priceDone struct {
	res prices.FetchResult
	err error
}

// Service is the usage-history plugin, store owner and report source.
type Service struct {
	now  func() time.Time
	pend *pending

	// recording gates HandleUsage. The only shared state HandleUsage reads besides pend.
	recording atomic.Bool

	// table is the current price table (catalog + overrides). Swapped by the loop.
	table atomic.Pointer[prices.Table]

	// rw: the loop takes Lock only to swap/close the store handle; readers hold
	// RLock for the duration of a read so a close never pulls the file out from under them.
	rw  sync.RWMutex
	cur status // written only by the loop under rw.Lock; read under rw.RLock

	// lifecycle (mu guards these four only)
	mu      sync.Mutex
	manager *coreauth.Manager
	cancel  context.CancelFunc
	done    chan struct{}

	syncCh  chan syncReq   // HTTP / quotaforecast -> loop
	priceCh chan priceDone // fetch goroutine -> loop

	// price fetch state, loop-owned
	sched      prices.Schedule
	priceWant  pricesWant
	lastErr    string
	etag       string
	catalog    *prices.Catalog // builtin, or the decoded DB cache / latest fetch if newer
	httpClient func(cfg *config.Config) *http.Client
}

var defaultService = newService()

// Default returns the process-wide usage history.
func Default() *Service { return defaultService }

// newService: now=time.Now, pend=newPending(), catalog=prices.Builtin(), table=NewTable(builtin, nil).
func newService() *Service { panic("not implemented") }

// Start records the manager, runs the first step synchronously (so records
// published right after Start are kept), then starts the run loop. No-op when
// running. Only called in the non-home branch of service_lifecycle.go.
func (s *Service) Start(ctx context.Context, manager *coreauth.Manager) { panic("not implemented") }

// Stop cancels the loop and waits (or until ctx is done). The loop's exit path
// does the final flush and closes the DB. Call AFTER usage.StopDefault(), which
// now waits for the usage worker to drain, so every queued record is in pending.
func (s *Service) Stop(ctx context.Context) { panic("not implemented") }

// HandleUsage implements usage.Plugin. Memory only: rollupOf + pending.add.
//
//	if !s.recording.Load() { return }
//	k, c, ok := rollupOf(ctx, rec, s.now()); if !ok { return }
//	s.pend.add(k, c)
func (s *Service) HandleUsage(ctx context.Context, rec coreusage.Record) { panic("not implemented") }

// run is the single writer.
//
// pseudocode:
//
//	defer close(done)
//	ticker := time.NewTicker(tickInterval)
//	for {
//	    select {
//	    case <-ctx.Done():
//	        s.recording.Store(false)
//	        if s.cur.phase == phaseOpen { s.flush(context.Background()) }  // local file write, not network
//	        s.closeStore(); s.cur.phase = phaseIdle
//	        drain syncCh replying idle so no caller waits forever; return
//	    case <-ticker.C:
//	        s.step(manager.RuntimeConfig(), true)
//	    case req := <-s.syncCh:
//	        s.step(manager.RuntimeConfig(), false); req.reply <- s.cur (copy)
//	    case d := <-s.priceCh:
//	        s.sched.InFlight = false
//	        if d.err != nil { s.sched.LastFail = now; s.lastErr = d.err.Error(); log warn; continue }
//	        if open { st.savePriceCache(d.res.Body, d.res.At, d.res.ETag) }   // 304: touch fetched_at only
//	        if d.res.Catalog != nil { s.catalog = d.res.Catalog; s.rebuildTable() }
//	        s.sched.LastOK = d.res.At; s.etag = d.res.ETag; s.lastErr = ""
//	    }
//	}
func (s *Service) run(ctx context.Context, done chan struct{}) { panic("not implemented") }

// step = one refresh: decide + apply the transition, flush, housekeeping.
//
//	w := wantOf(cfg)
//	apply(decide(s.cur, w, now))                               // see table
//	if w.prices.fingerprint != s.priceWant.fingerprint {
//	    url or remote changed -> s.sched = Schedule{LastOK: zero if url changed}
//	    s.priceWant = w.prices; s.sched.Disabled = !w.prices.remote; s.rebuildTable()
//	}
//	if s.cur.phase == phaseOpen {
//	    s.flush(ctx)                                           // tick AND sync: reports are current
//	    if tick && (now-cleanedAt >= cleanupInterval || cleanedFor != w.retention) {
//	        st.cleanupBefore(hourStart(now) - retention*86400); cleanedAt, cleanedFor = now, w.retention
//	    }
//	    if tick && s.sched.Due(now) {
//	        s.sched.InFlight = true
//	        go func() { res, err := prices.Fetch(ctx, client, url, etag, now); s.priceCh <- priceDone{res, err} }()
//	    }                                                      // network never runs on the loop
//	}
//	if dropped := pend.dropped; dropped > 0 { log warn once per tick }
func (s *Service) step(cfg *config.Config, tick bool) { panic("not implemented") }

// flush swaps pending out and upserts it. On error the batch is restored and the
// error logged (rate-limited); the next tick or sync retries. On success asOf = now.
func (s *Service) flush(ctx context.Context) { panic("not implemented") }

// open opens path and loads the price cache: if its catalog is newer than the
// current one it becomes s.catalog (table rebuilt) and sched.LastOK = fetched_at,
// so a restart does not refetch within the day. Swaps the handle in under rw.Lock.
func (s *Service) open(path string) error { panic("not implemented") }

// closeStore swaps the handle out under rw.Lock and closes it, logging errors.
func (s *Service) closeStore() { panic("not implemented") }

// rebuildTable = prices.NewTable(s.catalog, s.priceWant.overrides); logs each override error once per change.
func (s *Service) rebuildTable() { panic("not implemented") }

// sync asks the loop for a refresh + flush and returns the resulting status.
// If the loop is not running (before Start, after Stop, home mode) it returns
// an idle status (reason disabled, or home_mode from config). Waits at most
// until ctx is done (client gone), then returns idle/unavailable.
func (s *Service) sync(ctx context.Context) status { panic("not implemented") }

// withReader runs fn with the open store under rw.RLock. ok=false when not open.
func (s *Service) withReader(fn func(st *store, cur status)) (ok bool) { panic("not implemented") }
