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
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/quotaforecast"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/requestlog/prompt"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	tickInterval = time.Minute
	flushEvery   = 5 // ticks

	defaultQueryLimit   = 200
	maxQueryLimit       = 2000
	defaultSessionLimit = 100
	maxSessionLimit     = 1000
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
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	done := s.done
	s.mu.Unlock()
	// Refresh before the loop starts so records published right after Start
	// are captured and judged, not dropped by a still-closed gate.
	s.refreshEnabled(manager.RuntimeConfig())
	go s.run(runCtx, done)
}

// Stop ends the tick loop, which flushes the snapshot before returning.
// Waits until done or ctx expires.
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

// run is the tick loop: prune, resize and periodically flush.
func (s *Service) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	tick := 0
	for {
		s.tick(tick)
		tick++
		select {
		case <-ctx.Done():
			s.flush()
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) tick(tick int) {
	cfg := s.runtimeConfig()
	if !s.refreshEnabled(cfg) {
		return
	}
	s.ring.prune(s.now().Add(-time.Duration(retentionDays(cfg)) * 24 * time.Hour))
	s.ring.resize(maxEntries(cfg))
	s.tracker.resize(maxSessions(cfg))
	if tick%flushEvery == 0 {
		s.flush()
	}
}

// refreshEnabled reads cfg.RequestHistory, sets s.enabled and
// prompt.SetCaptureEnabled, resolves <auth-dir>/.request-history.gz, and loads
// the snapshot the first time a path is seen (ring.load then tracker.restore).
// Disabling clears capture immediately but keeps rows in memory, so re-enabling
// shows history again; rows are flushed before the flag drops.
func (s *Service) refreshEnabled(cfg *config.Config) bool {
	if cfg == nil || !cfg.RequestHistory.Enabled {
		s.disable("disabled")
		return false
	}
	if cfg.Home.Enabled {
		s.disable("home_mode")
		return false
	}
	dir, err := util.ResolveAuthDir(cfg.AuthDir)
	if err != nil {
		log.WithError(err).Warn("request history: cannot resolve auth dir")
		s.disable("unavailable")
		return false
	}
	path := filepath.Join(dir, snapshotFileName)
	s.mu.Lock()
	s.path = path
	s.reason = ""
	if s.loaded != path {
		// Held under mu so a concurrent refresh cannot enable recording before
		// the snapshot has replaced the ring.
		s.loaded = path
		lanes, errLoad := s.ring.load(path)
		if errLoad != nil {
			log.WithError(errLoad).Warn("request history: ignoring unreadable snapshot")
		}
		s.tracker.restore(lanes, s.now())
	}
	s.mu.Unlock()
	s.enabled.Store(true)
	prompt.SetCaptureEnabled(true)
	return true
}

func (s *Service) disable(reason string) {
	prompt.SetCaptureEnabled(false)
	if s.enabled.Load() {
		s.flush()
	}
	s.enabled.Store(false)
	s.mu.Lock()
	s.reason = reason
	s.mu.Unlock()
}

// flush writes the snapshot if the ring is dirty: copyForSave under RLock,
// tracker.export, encode + gzip + atomic rename outside both locks.
func (s *Service) flush() {
	s.mu.Lock()
	path := s.path
	s.mu.Unlock()
	if path == "" {
		return
	}
	rows, nextSeq, epoch, dirty := s.ring.copyForSave()
	if !dirty {
		return
	}
	now := s.now()
	data := buildSnapshot(rows, nextSeq, epoch, s.tracker.export(now), now)
	if err := writeSnapshot(&s.ring.fileMu, path, data); err != nil {
		log.WithError(err).Warn("request history: failed to save snapshot")
		s.ring.dirty.Store(true)
	}
}

// runtimeConfig returns manager.RuntimeConfig() or nil before Start.
func (s *Service) runtimeConfig() *config.Config {
	s.mu.Lock()
	manager := s.manager
	s.mu.Unlock()
	if manager == nil {
		return nil
	}
	return manager.RuntimeConfig()
}

func (s *Service) disabledReason() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason
}

// Query returns entries matching f, newest-first unless f.Asc.
func (s *Service) Query(f Filter) Page {
	cfg := s.runtimeConfig()
	if !s.refreshEnabled(cfg) {
		return Page{Reason: s.disabledReason(), Keys: map[string]string{}, Entries: []Entry{}}
	}
	limit := clampInt(f.Limit, defaultQueryLimit, 1, maxQueryLimit)
	// Read the cursor before scanning: a row appended during the scan has a seq
	// above this cursor and arrives on the next poll instead of being skipped.
	oldest, newest, epoch := s.ring.bounds()
	lo, hi := uint64(0), newest
	if f.Since > 0 {
		lo = f.Since + 1
	}
	if f.Before > 0 {
		hi = min(hi, f.Before-1)
	}
	m := newRowMatcher(f)
	entries := []Entry{}
	more := false
	matched := 0
	s.ring.scan(lo, hi, f.Asc, func(r *row) bool {
		if !m.match(r) {
			return true
		}
		matched++
		if matched > limit {
			more = true
			return false
		}
		entries = append(entries, entryOf(r))
		return true
	})
	keys := make(map[string]bool, 4)
	for i := range entries {
		keys[entries[i].KeyHash] = true
	}
	return Page{Enabled: true, Epoch: epoch, Cursor: newest, Oldest: oldest, More: more, Keys: maskedKeys(cfg, keys), Entries: entries}
}

// maskedKeys labels the key hashes present with the masked configured key.
func maskedKeys(cfg *config.Config, present map[string]bool) map[string]string {
	out := make(map[string]string, len(present))
	for _, key := range cfg.APIKeys {
		if hash := quotaforecast.HashClientKey(key); present[hash] {
			out[hash] = util.HideAPIKey(strings.TrimSpace(key))
		}
	}
	return out
}

// rowMatcher is Filter with its string fields parsed once per query.
type rowMatcher struct {
	f          Filter
	fromMs     int64
	toMs       int64
	wantFailed int8 // -1 any, 0 ok only, 1 failed only
	status     int16
	verdicts   map[VerdictKind]bool
}

func newRowMatcher(f Filter) rowMatcher {
	m := rowMatcher{f: f, wantFailed: -1}
	if !f.From.IsZero() {
		m.fromMs = f.From.UnixMilli()
	}
	if !f.To.IsZero() {
		m.toMs = f.To.UnixMilli()
	}
	switch status := strings.ToLower(strings.TrimSpace(f.Status)); status {
	case "":
	case "ok":
		m.wantFailed = 0
	case "failed":
		m.wantFailed = 1
	default:
		code, err := strconv.Atoi(status)
		if err != nil {
			code = -1
		}
		m.status = int16(code)
	}
	if f.Verdict != "" {
		m.verdicts = make(map[VerdictKind]bool)
		for _, name := range strings.Split(f.Verdict, ",") {
			if kind, ok := parseVerdictKind(name); ok {
				m.verdicts[kind] = true
			}
		}
	}
	return m
}

func (m *rowMatcher) match(r *row) bool {
	f := &m.f
	if f.Session != "" && r.sessionID != f.Session {
		return false
	}
	if f.KeyHash != "" && r.keyHash != f.KeyHash {
		return false
	}
	if f.AuthID != "" && r.authID != f.AuthID {
		return false
	}
	if f.Model != "" && r.model != f.Model {
		return false
	}
	if m.fromMs != 0 && r.atMs < m.fromMs {
		return false
	}
	if m.toMs != 0 && r.atMs >= m.toMs {
		return false
	}
	failed := r.flags&rowFailed != 0
	switch m.wantFailed {
	case 0:
		if failed {
			return false
		}
	case 1:
		if !failed {
			return false
		}
	}
	if m.status != 0 && r.status != m.status {
		return false
	}
	if m.verdicts != nil && !m.verdicts[r.verdict.Kind] {
		return false
	}
	return true
}

// sessionAgg accumulates one session during the Sessions scan (newest row first).
type sessionAgg struct {
	id           string
	keyHash      string
	agent        string
	firstMs      int64
	lastMs       int64
	requests     int
	failed       int
	models       map[string]bool
	auths        map[string]bool
	keys         map[string]bool
	verdicts     [5]int
	tokens       SessionTokens
	judgedRead   int64
	judgedPrompt int64
	lost         int64
	causes       map[Cause]int
	lastMiss     *MissRef
}

func (a *sessionAgg) add(r *row) {
	a.requests++
	a.firstMs = r.atMs
	if r.flags&rowFailed != 0 {
		a.failed++
	}
	a.models[r.model] = true
	a.auths[r.authID] = true
	a.keys[r.keyHash] = true
	a.verdicts[r.verdict.Kind]++
	prompt := int64(r.uncached) + int64(r.read) + int64(r.write)
	a.tokens.Input += int64(r.uncached)
	a.tokens.CacheRead += int64(r.read)
	a.tokens.CacheWrite += int64(r.write)
	a.tokens.Output += int64(r.output)
	a.tokens.Reasoning += int64(r.reasoning)
	a.tokens.Prompt += prompt
	switch r.verdict.Kind {
	case VerdictHit:
		a.judgedRead += int64(r.read)
		a.judgedPrompt += prompt
	case VerdictPartial, VerdictMiss:
		a.judgedRead += int64(r.read)
		a.judgedPrompt += prompt
		a.lost += max(int64(r.verdict.Expected)-int64(r.read), 0)
		a.causes[r.verdict.Primary]++
		if a.lastMiss == nil {
			a.lastMiss = &MissRef{Seq: r.seq, At: time.UnixMilli(r.atMs).UTC(), Cause: causeNames[r.verdict.Primary], Segment: segmentOf(r.verdict)}
		}
	}
}

func (a *sessionAgg) view() Session {
	s := Session{
		SessionID:  a.id,
		KeyHash:    a.keyHash,
		Agent:      a.agent,
		FirstAt:    time.UnixMilli(a.firstMs).UTC(),
		LastAt:     time.UnixMilli(a.lastMs).UTC(),
		Requests:   a.requests,
		Failed:     a.failed,
		Models:     sortedKeys(a.models),
		AuthIDs:    sortedKeys(a.auths),
		Verdicts:   make(map[string]int, 5),
		Tokens:     a.tokens,
		LostTokens: a.lost,
		Causes:     make(map[string]int, len(a.causes)),
		LastMiss:   a.lastMiss,
	}
	for kind, n := range a.verdicts {
		if n > 0 {
			s.Verdicts[verdictNames[kind]] = n
		}
	}
	if a.judgedPrompt > 0 {
		s.HitRatio = float64(a.judgedRead) / float64(a.judgedPrompt)
	}
	top, topCount := CauseNone, 0
	for cause, n := range a.causes {
		s.Causes[causeNames[cause]] = n
		if n > topCount || (n == topCount && cause < top) {
			top, topCount = cause, n
		}
	}
	if top != CauseNone {
		s.TopCause = causeNames[top]
	}
	return s
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		if k != "" {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Sessions aggregates entries per session with one newest-first scan.
// Computed on demand rather than kept incrementally: one owner (the ring), no
// second structure to keep consistent with pruning, and the scan is ~1-3 ms at
// the default size.
func (s *Service) Sessions(f SessionFilter) SessionPage {
	cfg := s.runtimeConfig()
	if !s.refreshEnabled(cfg) {
		return SessionPage{Reason: s.disabledReason(), Keys: map[string]string{}, Sessions: []Session{}}
	}
	limit := clampInt(f.Limit, defaultSessionLimit, 1, maxSessionLimit)
	var fromMs, toMs int64
	if !f.From.IsZero() {
		fromMs = f.From.UnixMilli()
	}
	if !f.To.IsZero() {
		toMs = f.To.UnixMilli()
	}
	_, newest, epoch := s.ring.bounds()
	aggs := make(map[string]*sessionAgg)
	order := make([]*sessionAgg, 0, 64)
	s.ring.scan(0, newest, false, func(r *row) bool {
		if r.sessionID == "" || (fromMs != 0 && r.atMs < fromMs) || (toMs != 0 && r.atMs >= toMs) {
			return true
		}
		a := aggs[r.sessionID]
		if a == nil {
			a = &sessionAgg{id: r.sessionID, keyHash: r.keyHash, agent: r.agent, lastMs: r.atMs,
				models: make(map[string]bool, 2), auths: make(map[string]bool, 2), keys: make(map[string]bool, 1), causes: make(map[Cause]int)}
			aggs[r.sessionID] = a
			order = append(order, a)
		}
		a.add(r)
		return true
	})
	sessions := make([]Session, 0, min(len(order), limit))
	present := make(map[string]bool, 4)
	for _, a := range order {
		if (f.KeyHash != "" && !a.keys[f.KeyHash]) || (f.AuthID != "" && !a.auths[f.AuthID]) || (f.Model != "" && !a.models[f.Model]) {
			continue
		}
		if len(sessions) == limit {
			break
		}
		sessions = append(sessions, a.view())
		present[a.keyHash] = true
	}
	return SessionPage{Enabled: true, Epoch: epoch, Cursor: newest, Keys: maskedKeys(cfg, present), Sessions: sessions}
}

// clamping helpers: zero -> default, then bound to [1, max].
func maxEntries(cfg *config.Config) int {
	if cfg == nil {
		return defaultMaxEntries
	}
	return clampInt(cfg.RequestHistory.MaxEntries, defaultMaxEntries, 1, maxMaxEntries)
}

func retentionDays(cfg *config.Config) int {
	if cfg == nil {
		return defaultRetentionDays
	}
	return clampInt(cfg.RequestHistory.RetentionDays, defaultRetentionDays, 1, maxRetentionDays)
}

func maxSessions(cfg *config.Config) int {
	if cfg == nil {
		return defaultMaxSessions
	}
	return clampInt(cfg.RequestHistory.MaxSessions, defaultMaxSessions, 1, maxMaxSessions)
}

func clampInt(v, fallback, lo, hi int) int {
	if v <= 0 {
		return fallback
	}
	return max(lo, min(hi, v))
}
