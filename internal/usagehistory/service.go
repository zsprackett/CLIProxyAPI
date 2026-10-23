package usagehistory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory/prices"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

const (
	tickInterval         = time.Minute
	cleanupInterval      = 24 * time.Hour
	reopenBackoff        = time.Minute // phaseBroken retries at most this often
	defaultRetentionDays = 365
	maxRetentionDays     = 3650
)

// phase is the service's lifecycle state. Owned by the run loop; readers see a
// copy through sync().
type phase uint8

const (
	phaseIdle   phase = iota // before Start / after Stop: no DB, not recording
	phaseOff                 // running; config disabled or home mode: no DB, not recording
	phaseOpen                // running; DB open at path: recording, flushing each tick and sync
	phaseBroken              // running; enabled but the DB cannot be opened at path: recording
	//                          into pending (bounded), retrying the open every reopenBackoff
)

// want is what the config asks for, computed by wantOf.
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

// status is the run loop's owned state. Readers get a copy from sync().
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

	// Snapshots of loop-owned price state for readers.
	catalog   CatalogInfo
	overrides []prices.Override
}

// action is what a transition does.
type action uint8

const (
	actNone       action = iota
	actOpen              // recording on; open(path): ok -> phaseOpen, err -> phaseBroken
	actRetry             // phaseBroken and backoff elapsed: same as actOpen
	actFlushClose        // recording off; flush pending to the open DB; close -> phaseOff
	actMove              // flush to old path, close, open new path (auth dir changed)
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
//	| any running| Stop              | (run exit)  | flush if Open, close -> Idle |
//
// The recording flag is true exactly in Open and Broken. It is set before an
// open (so no record published after enabling is lost) and cleared before the
// final flush of a flushClose (so nothing lands in pending after it).
func decide(cur status, w want, now time.Time) action {
	switch cur.phase {
	case phaseOff:
		if w.on {
			return actOpen
		}
	case phaseOpen:
		switch {
		case !w.on:
			return actFlushClose
		case w.path != cur.path:
			return actMove
		}
	case phaseBroken:
		switch {
		case !w.on:
			return actDiscard
		case w.path != cur.path:
			return actOpen
		case now.Sub(cur.lastOpen) >= reopenBackoff:
			return actRetry
		}
	}
	return actNone
}

// wantOf reads the pull-model config. Pure apart from util.ResolveAuthDir.
func wantOf(cfg *config.Config) want {
	if cfg == nil {
		return want{reason: ReasonDisabled, retention: defaultRetentionDays, prices: pricesWantOf(config.UsagePricesConfig{})}
	}
	w := want{retention: clampRetention(cfg.UsageHistory.RetentionDays), prices: pricesWantOf(cfg.UsageHistory.Prices)}
	switch {
	case !cfg.UsageHistory.Enabled:
		w.reason = ReasonDisabled
	case cfg.Home.Enabled:
		w.reason = ReasonHomeMode
	default:
		dir, err := util.ResolveAuthDir(cfg.AuthDir)
		if err != nil || dir == "" {
			w.reason = ReasonUnavailable
			break
		}
		w.on, w.path = true, filepath.Join(dir, dbFileName)
	}
	return w
}

func clampRetention(days int) int {
	switch {
	case days <= 0:
		return defaultRetentionDays
	case days > maxRetentionDays:
		return maxRetentionDays
	}
	return days
}

func pricesWantOf(c config.UsagePricesConfig) pricesWant {
	w := pricesWant{remote: !c.DisableRemote, url: strings.TrimSpace(c.RemoteURL)}
	if w.url == "" {
		w.url = prices.DefaultURL
	}
	for _, m := range c.Models {
		w.overrides = append(w.overrides, prices.Override{
			Model:    m.Model,
			Provider: m.Provider,
			SameAs:   m.SameAs,
			PerMTok: prices.OverrideRates{
				Input: m.Input, Output: m.Output, Reasoning: m.Reasoning,
				CacheRead: m.CacheRead, CacheWrite5m: m.CacheWrite5m, CacheWrite1h: m.CacheWrite1h,
			},
		})
	}
	raw, err := json.Marshal(struct {
		Remote    bool
		URL       string
		Overrides []prices.Override
	}{w.remote, w.url, w.overrides})
	if err != nil {
		raw = []byte(err.Error())
	}
	sum := sha256.Sum256(raw)
	w.fingerprint = hex.EncodeToString(sum[:])
	return w
}

// syncReq asks the run loop to refresh config and flush pending now.
type syncReq struct{ reply chan status }

// priceDone is a finished background fetch, handed back to the loop to persist.
type priceDone struct {
	res prices.FetchResult
	err error
}

// fetchFunc downloads the remote catalog. The default wraps prices.Fetch with
// prices.NewClient(); tests replace it.
type fetchFunc func(ctx context.Context, url, etag string, now time.Time) (prices.FetchResult, error)

// Service is the usage-history plugin, store owner and report source.
type Service struct {
	now  func() time.Time
	pend *pending

	// recording gates HandleUsage. The only shared state HandleUsage reads besides pend.
	recording atomic.Bool

	// table is the current price table (catalog + overrides). Swapped by the loop.
	table atomic.Pointer[prices.Table]

	// rw: the loop takes Lock to change cur; readers hold RLock for the duration
	// of a read so a close never pulls the file out from under them.
	rw  sync.RWMutex
	cur status

	// lifecycle (mu guards these four only)
	mu      sync.Mutex
	manager *coreauth.Manager
	cancel  context.CancelFunc
	done    chan struct{}

	syncCh  chan syncReq   // HTTP / quotaforecast -> loop
	priceCh chan priceDone // fetch goroutine -> loop; cap 1, at most one fetch in flight

	// newTicker is the run loop's clock; tests drive ticks by hand.
	newTicker func() (<-chan time.Time, func())
	fetch     fetchFunc

	// loop-owned
	sched          prices.Schedule
	priceWant      pricesWant
	priceWantSet   bool
	lastErr        string
	etag           string
	catalog        *prices.Catalog // builtin, or the cached / latest fetch if newer
	snapshotDate   string
	tableErrs      string // last logged NewTable errors, so each change logs once
	lastOpenErr    string
	lastFlushErr   string
	lastCleanupErr string
}

var defaultService = newService()

// Default returns the process-wide usage history.
func Default() *Service { return defaultService }

func newService() *Service {
	builtin := prices.Builtin()
	s := &Service{
		now:          time.Now,
		pend:         newPending(),
		syncCh:       make(chan syncReq),
		priceCh:      make(chan priceDone, 1),
		catalog:      builtin,
		snapshotDate: builtin.Generated.UTC().Format(dateLayout),
		cur:          status{reason: ReasonDisabled, retention: defaultRetentionDays},
		newTicker: func() (<-chan time.Time, func()) {
			t := time.NewTicker(tickInterval)
			return t.C, t.Stop
		},
		fetch: func(ctx context.Context, url, etag string, now time.Time) (prices.FetchResult, error) {
			return prices.Fetch(ctx, prices.NewClient(), url, etag, now)
		},
	}
	table, _ := prices.NewTable(builtin, nil)
	s.table.Store(table)
	return s
}

// Start records the manager, runs the first step synchronously (so records
// published right after Start are kept), then starts the run loop. No-op when
// running. Only called in the non-home branch of service_lifecycle.go.
//
// The loop ignores ctx cancellation: Run cancels its context before the deferred
// Shutdown drains the usage queue, and records drained then must still reach
// the file. Only Stop ends the loop.
func (s *Service) Start(ctx context.Context, manager *coreauth.Manager) {
	if s == nil || manager == nil {
		return
	}
	s.mu.Lock()
	s.manager = manager
	if s.cancel != nil {
		s.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	s.cancel = cancel
	s.done = make(chan struct{})
	done := s.done
	s.mu.Unlock()

	s.setStatus(func(c *status) { c.phase = phaseOff })
	s.step(runCtx, manager.RuntimeConfig(), false)
	go s.run(runCtx, done)
}

// Stop cancels the loop and waits (or until ctx is done). The loop's exit path
// does the final flush and closes the DB. Call after usage.StopDefault(), which
// waits for the usage worker to drain, so every queued record is in pending.
func (s *Service) Stop(ctx context.Context) {
	if s == nil {
		return
	}
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.cancel, s.done = nil, nil
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// HandleUsage implements usage.Plugin. Memory only: rollupOf + pending.add.
func (s *Service) HandleUsage(ctx context.Context, rec coreusage.Record) {
	if s == nil || !s.recording.Load() {
		return
	}
	k, c, ok := rollupOf(ctx, rec, s.now())
	if !ok {
		return
	}
	s.pend.add(k, c)
}

func (s *Service) config() *config.Config {
	s.mu.Lock()
	manager := s.manager
	s.mu.Unlock()
	if manager == nil {
		return nil
	}
	return manager.RuntimeConfig()
}

// run is the single writer: every SQLite write happens on this goroutine.
func (s *Service) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	tick, stopTicker := s.newTicker()
	defer stopTicker()
	for {
		select {
		case <-ctx.Done():
			s.exit()
			return
		case <-tick:
			s.step(ctx, s.config(), true)
		case req := <-s.syncCh:
			s.step(ctx, s.config(), false)
			req.reply <- s.snapshot()
		case d := <-s.priceCh:
			s.applyPrice(ctx, d)
		}
	}
}

// exit is the loop's last act: stop recording, flush, close, go Idle.
func (s *Service) exit() {
	s.recording.Store(false)
	switch s.cur.phase {
	case phaseOpen:
		// A local file write; the cancelled loop ctx must not abort it.
		s.flush(context.Background())
	case phaseBroken:
		if batch, _ := s.pend.swap(); len(batch) > 0 {
			log.Warnf("usage history: stopping with the database unavailable; %d pending rollup rows are lost", len(batch))
		}
	}
	s.closeStore()
	s.setStatus(func(c *status) { c.phase, c.path = phaseIdle, "" })
}

// step is one refresh: decide and apply the transition, flush, housekeeping.
func (s *Service) step(ctx context.Context, cfg *config.Config, tick bool) {
	// Drain a finished fetch first so a sync right after it sees the new table.
	select {
	case d := <-s.priceCh:
		s.applyPrice(ctx, d)
	default:
	}
	now := s.now()
	w := wantOf(cfg)
	// Prices before the transition: an open loads the price cache, which must
	// not be undone by a first-time URL setup.
	s.updatePriceWant(w.prices)
	s.apply(ctx, decide(s.cur, w, now), w, now)
	if s.cur.retention != w.retention {
		s.setStatus(func(c *status) { c.retention = w.retention })
	}
	if s.cur.phase == phaseOff && s.cur.reason != w.reason && !w.on {
		s.setStatus(func(c *status) { c.reason = w.reason })
	}
	if s.cur.phase != phaseOpen {
		return
	}
	s.flush(ctx)
	if !tick {
		return
	}
	if now.Sub(s.cur.cleanedAt) >= cleanupInterval || s.cur.cleanedFor != w.retention {
		s.cleanup(ctx, now, w.retention)
	}
	if s.sched.Due(now) {
		s.startFetch(ctx, now)
	}
}

func (s *Service) apply(ctx context.Context, a action, w want, now time.Time) {
	switch a {
	case actOpen, actRetry:
		s.open(ctx, w.path, now)
	case actMove:
		s.flush(ctx)
		s.closeStore()
		s.open(ctx, w.path, now)
	case actFlushClose:
		s.recording.Store(false)
		s.flush(ctx)
		s.closeStore()
		s.setStatus(func(c *status) { c.phase, c.path, c.reason = phaseOff, "", w.reason })
	case actDiscard:
		s.recording.Store(false)
		s.pend.discard()
		s.setStatus(func(c *status) { c.phase, c.path, c.reason = phaseOff, "", w.reason })
	}
}

// open opens path and loads the price cache: if its catalog is newer than the
// current one it becomes s.catalog, and sched.LastOK = fetched_at so a restart
// does not refetch within the day. On failure the service is Broken at path.
func (s *Service) open(ctx context.Context, path string, now time.Time) {
	s.recording.Store(true)
	st, err := openStore(ctx, path)
	if err != nil {
		if msg := err.Error(); msg != s.lastOpenErr {
			s.lastOpenErr = msg
			log.WithError(err).Warnf("usage history: cannot open %s; recording in memory and retrying every %s", path, reopenBackoff)
		}
		s.setStatus(func(c *status) {
			c.phase, c.path, c.reason, c.st, c.lastOpen = phaseBroken, path, ReasonUnavailable, nil, now
		})
		return
	}
	if s.lastOpenErr != "" {
		log.Infof("usage history: opened %s", path)
	}
	s.lastOpenErr = ""
	s.loadPriceCache(ctx, st)
	s.setStatus(func(c *status) {
		c.phase, c.path, c.reason, c.st, c.lastOpen = phaseOpen, path, ReasonNone, st, now
		c.cleanedAt, c.cleanedFor = time.Time{}, 0
	})
}

func (s *Service) loadPriceCache(ctx context.Context, st *store) {
	body, fetchedAt, etag, ok, err := st.loadPriceCache(ctx)
	if err != nil {
		log.WithError(err).Warn("usage history: ignoring the cached price catalog")
		return
	}
	if !ok {
		return
	}
	cat, err := prices.Decode(body)
	if err != nil {
		log.WithError(err).Warn("usage history: ignoring an unreadable cached price catalog")
		return
	}
	if cat.Generated.After(s.catalog.Generated) {
		s.catalog = cat
		s.rebuildTable()
	}
	if fetchedAt.After(s.sched.LastOK) {
		s.sched.LastOK = fetchedAt
	}
	s.etag = etag
}

// closeStore swaps the handle out under rw.Lock and closes it, logging errors.
func (s *Service) closeStore() {
	s.rw.Lock()
	st := s.cur.st
	s.cur.st = nil
	s.rw.Unlock()
	if st == nil {
		return
	}
	if err := st.close(); err != nil {
		log.WithError(err).Warn("usage history: close database")
	}
}

// flush swaps pending out and upserts it. On error the batch is restored and the
// error logged once per distinct error; the next tick or sync retries.
func (s *Service) flush(ctx context.Context) {
	batch, dropped := s.pend.swap()
	if dropped > 0 {
		log.Warnf("usage history: dropped %d records because %d rollup rows were pending", dropped, maxPendingKeys)
	}
	st := s.cur.st
	if st == nil {
		s.pend.restore(batch)
		return
	}
	if err := st.upsert(ctx, batch); err != nil {
		s.pend.restore(batch)
		if msg := err.Error(); msg != s.lastFlushErr {
			s.lastFlushErr = msg
			log.WithError(err).Warn("usage history: flush failed; will retry")
		}
		return
	}
	s.lastFlushErr = ""
	now := s.now()
	s.setStatus(func(c *status) { c.asOf = now })
}

func (s *Service) cleanup(ctx context.Context, now time.Time, retention int) {
	cutoff := hourStart(now) - int64(retention)*86400
	deleted, err := s.cur.st.cleanupBefore(ctx, cutoff)
	if err != nil {
		if msg := err.Error(); msg != s.lastCleanupErr {
			s.lastCleanupErr = msg
			log.WithError(err).Warn("usage history: retention cleanup failed")
		}
		return
	}
	s.lastCleanupErr = ""
	if deleted > 0 {
		log.Infof("usage history: removed %d rollup rows older than %d days", deleted, retention)
	}
	s.setStatus(func(c *status) { c.cleanedAt, c.cleanedFor = now, retention })
}

// updatePriceWant applies a changed price config: schedule, then table.
func (s *Service) updatePriceWant(w pricesWant) {
	if s.priceWantSet && w.fingerprint == s.priceWant.fingerprint {
		return
	}
	if s.priceWantSet && w.url != s.priceWant.url {
		s.sched = prices.Schedule{InFlight: s.sched.InFlight}
		s.etag = ""
	}
	s.priceWant, s.priceWantSet = w, true
	s.sched.Disabled = !w.remote
	s.rebuildTable()
}

// rebuildTable swaps in prices.NewTable(catalog, overrides) and logs override
// errors once per distinct set.
func (s *Service) rebuildTable() {
	table, errs := prices.NewTable(s.catalog, s.priceWant.overrides)
	s.table.Store(table)
	msg := errors.Join(errs...)
	text := ""
	if msg != nil {
		text = msg.Error()
	}
	if text != s.tableErrs {
		s.tableErrs = text
		for _, err := range errs {
			log.WithError(err).Warn("usage history: skipping a price override")
		}
	}
}

// startFetch runs the remote fetch off the loop; the result comes back on priceCh.
func (s *Service) startFetch(ctx context.Context, now time.Time) {
	s.sched.InFlight = true
	fetch, url, etag := s.fetch, s.priceWant.url, s.etag
	go func() {
		res, err := fetch(ctx, url, etag, now)
		s.priceCh <- priceDone{res: res, err: err}
	}()
}

func (s *Service) applyPrice(ctx context.Context, d priceDone) {
	s.sched.InFlight = false
	if d.err != nil {
		s.sched.LastFail = s.now()
		s.lastErr = d.err.Error()
		log.WithError(d.err).Warn("usage history: price catalog fetch failed; keeping the current catalog")
		return
	}
	if st := s.cur.st; st != nil {
		if err := st.savePriceCache(ctx, d.res.Body, d.res.At, d.res.ETag); err != nil {
			log.WithError(err).Warn("usage history: cache the price catalog")
		}
	}
	if d.res.Catalog != nil {
		s.catalog = d.res.Catalog
		s.rebuildTable()
	}
	s.sched.LastOK, s.etag, s.lastErr = d.res.At, d.res.ETag, ""
}

func (s *Service) setStatus(fn func(*status)) {
	s.rw.Lock()
	fn(&s.cur)
	s.rw.Unlock()
}

// snapshot copies the loop state for a reader, with the price state attached.
func (s *Service) snapshot() status {
	cur := s.cur
	cur.catalog = s.catalogInfo()
	cur.overrides = s.priceWant.overrides
	return cur
}

func (s *Service) catalogInfo() CatalogInfo {
	info := CatalogInfo{
		Source:       string(prices.SourceBuiltin),
		SnapshotDate: s.snapshotDate,
		LastError:    s.lastErr,
		Remote:       !s.sched.Disabled,
	}
	if t := s.table.Load(); t != nil && t.Catalog() != nil && t.Catalog().Source != "" {
		info.Source = string(t.Catalog().Source)
	}
	if !s.sched.LastOK.IsZero() {
		at := s.sched.LastOK
		info.FetchedAt = &at
	}
	if next := s.sched.Next(); info.Remote && !next.IsZero() {
		info.NextFetchAt = &next
	}
	return info
}

// sync asks the loop for a refresh and flush, and returns the resulting status.
// If the loop is not running (before Start, after Stop, home mode) it returns an
// idle status. Waits at most until ctx is done (client gone).
func (s *Service) sync(ctx context.Context) status {
	s.mu.Lock()
	done, manager := s.done, s.manager
	s.mu.Unlock()
	idle := status{phase: phaseIdle, reason: ReasonDisabled}
	if done == nil {
		if manager != nil {
			if w := wantOf(manager.RuntimeConfig()); !w.on {
				idle.reason = w.reason
			}
		}
		return idle
	}
	req := syncReq{reply: make(chan status, 1)}
	select {
	case s.syncCh <- req:
	case <-done:
		return idle
	case <-ctx.Done():
		return status{phase: phaseIdle, reason: ReasonUnavailable}
	}
	select {
	case cur := <-req.reply:
		return cur
	case <-ctx.Done():
		return status{phase: phaseIdle, reason: ReasonUnavailable}
	}
}

// withReader runs fn with the open store under rw.RLock. ok=false when not open.
func (s *Service) withReader(fn func(st *store)) bool {
	s.rw.RLock()
	defer s.rw.RUnlock()
	if s.cur.phase != phaseOpen || s.cur.st == nil {
		return false
	}
	fn(s.cur.st)
	return true
}

// reasonOf maps a status to the reason a reader reports.
func reasonOf(cur status) Reason {
	switch cur.phase {
	case phaseOpen:
		return ReasonNone
	case phaseBroken:
		return ReasonUnavailable
	}
	if cur.reason == ReasonNone {
		return ReasonDisabled
	}
	return cur.reason
}
