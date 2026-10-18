package requestlog

import (
	"container/list"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/requestlog/prompt"
)

// tracker owns the per-session cache baselines. It is the only holder of Prints.
//
// Writers: the usage worker goroutine (observe), load at startup.
// Readers: the tick goroutine (snapshot for persistence).
// HTTP handlers never touch it: the session summary endpoint is computed from
// entries, so evicting a baseline never changes what the UI shows for the past.
//
// Lock: mu guards everything below. It is never held together with ring.mu.
type tracker struct {
	mu       sync.Mutex
	sessions map[string]*session // key: session ID
	lru      *list.List          // front = most recently observed; values are *session

	hashes      int // sum of Print.Size() over all lanes
	maxSessions int // config max-sessions, default 1000
	hashBudget  int // fixed 1<<20 hashes = 8 MiB of segment hashes
}

// session holds up to maxLanes baselines, one per model. Lanes exist because
// Claude Code interleaves Haiku helper calls with the main model in ONE session:
// a single "previous request" baseline would report every alternation as
// "model changed" while the main model's cache stays warm.
type session struct {
	id    string
	elem  *list.Element
	lanes []lane // len <= maxLanes, most recently touched first
}

const maxLanes = 4

// lane is the baseline for one (session, model): the last successful attempt.
type lane struct {
	model        string // interned
	authID       string // interned
	touchedAt    time.Time
	cachedPrefix int64 // Claude: read+write; Codex: promptTotal rounded down to 128
	ttl          time.Duration
	print        *prompt.Print
}

// laneUpdate is what judge asks the tracker to store after a verdict.
type laneUpdate struct {
	model        string
	authID       string
	touchedAt    time.Time
	cachedPrefix int64
	ttl          time.Duration
	print        *prompt.Print
}

func newTracker(maxSessions int) *tracker { panic("not implemented") }

// observe judges obs and applies the resulting lane update atomically, so two
// records of one session can never be judged against a half-updated baseline.
// The usage worker is the only caller, so contention is nil; the lock exists for
// the snapshot reader.
//
//	mu.Lock()
//	sess := sessions[obs.sessionID]           // may be nil
//	v, upd := judge(obs, sess)
//	if upd != nil && obs.sessionID != "":
//	    if sess == nil: sess = new session; sessions[id] = sess; sess.elem = lru.PushFront(sess)
//	    replace or insert the lane for upd.model (drop the least recently touched lane
//	        when len(lanes) == maxLanes); hashes += new.Size() - old.Size()
//	    lru.MoveToFront(sess.elem)
//	    evictLocked()
//	mu.Unlock()
//	return v
func (t *tracker) observe(obs observation) Verdict { panic("not implemented") }

// baseline returns the same-model lane when present (sameModel=true), otherwise
// the most recently touched lane of any model (sameModel=false), or nil.
// A nil *session returns nil.
func (s *session) baseline(model string) (base *lane, sameModel bool) { panic("not implemented") }

// evictLocked drops least-recently-observed sessions while
// len(sessions) > maxSessions || hashes > hashBudget.
// Effect of eviction: that session's next request is judged Cold. Entries are untouched.
func (t *tracker) evictLocked() { panic("not implemented") }

// resize applies a hot-reloaded max-sessions value and evicts if needed.
func (t *tracker) resize(maxSessions int) { panic("not implemented") }

// persistedLane is the snapshot form of a lane.
type persistedLane struct {
	Session      string `json:"s"`
	Model        string `json:"m"`
	AuthID       string `json:"a"`
	TouchedAt    int64  `json:"t"` // unix ms
	CachedPrefix int64  `json:"p"`
	TTLSec       int64  `json:"ttl"`
	Print        []byte `json:"f"` // prompt.Print.MarshalBinary, base64 in JSON
}

// export returns lanes touched within maxBaselineAge of now. Older lanes are
// useless after a restart anywhere: any request judged against them would be an
// idle-gap miss, which "cold" already conveys. This keeps the snapshot small
// (typically tens of lanes) while restarts during active work keep their verdicts.
func (t *tracker) export(now time.Time) []persistedLane { panic("not implemented") }

// maxBaselineAge is the longest TTL any provider offers through this proxy (Claude 1h).
const maxBaselineAge = time.Hour

// restore loads persisted lanes, skipping malformed prints and lanes older than
// maxBaselineAge. Called once per snapshot path, before the worker delivers records.
func (t *tracker) restore(lanes []persistedLane, now time.Time) { panic("not implemented") }
