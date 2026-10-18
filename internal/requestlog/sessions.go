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

func newTracker(maxSessions int) *tracker {
	return &tracker{
		sessions:    make(map[string]*session),
		lru:         list.New(),
		maxSessions: max(maxSessions, 1),
		hashBudget:  trackerHashBudget,
	}
}

// observe judges obs and applies the resulting lane update atomically, so two
// records of one session can never be judged against a half-updated baseline.
// The usage worker is the only caller, so contention is nil; the lock exists for
// the snapshot reader.
func (t *tracker) observe(obs observation) Verdict {
	t.mu.Lock()
	defer t.mu.Unlock()
	sess := t.sessions[obs.sessionID]
	v, upd := judge(obs, sess)
	if upd == nil || obs.sessionID == "" {
		return v
	}
	if sess == nil {
		sess = t.addLocked(obs.sessionID)
	}
	t.setLaneLocked(sess, lane(*upd))
	t.lru.MoveToFront(sess.elem)
	t.evictLocked()
	return v
}

func (t *tracker) addLocked(id string) *session {
	sess := &session{id: id}
	sess.elem = t.lru.PushFront(sess)
	t.sessions[id] = sess
	return sess
}

// setLaneLocked replaces or inserts the lane for l.model at the front, dropping
// the least recently touched lane when the session already holds maxLanes.
func (t *tracker) setLaneLocked(sess *session, l lane) {
	for i := range sess.lanes {
		if sess.lanes[i].model == l.model {
			t.hashes -= sess.lanes[i].print.Size()
			sess.lanes = append(sess.lanes[:i], sess.lanes[i+1:]...)
			break
		}
	}
	if len(sess.lanes) == maxLanes {
		t.hashes -= sess.lanes[maxLanes-1].print.Size()
		sess.lanes = sess.lanes[:maxLanes-1]
	}
	t.hashes += l.print.Size()
	sess.lanes = append([]lane{l}, sess.lanes...)
}

// baseline returns the lane for model, or nil. Prompt caches are per model, so
// another model's lane is never a baseline. A nil *session returns nil.
func (s *session) baseline(model string) *lane {
	if s == nil {
		return nil
	}
	for i := range s.lanes {
		if s.lanes[i].model == model {
			return &s.lanes[i]
		}
	}
	return nil
}

// evictLocked drops least-recently-observed sessions while
// len(sessions) > maxSessions || hashes > hashBudget.
// Effect of eviction: that session's next request is judged Cold. Entries are untouched.
func (t *tracker) evictLocked() {
	for t.lru.Len() > 0 && (len(t.sessions) > t.maxSessions || t.hashes > t.hashBudget) {
		sess := t.lru.Back().Value.(*session)
		for i := range sess.lanes {
			t.hashes -= sess.lanes[i].print.Size()
		}
		t.lru.Remove(sess.elem)
		delete(t.sessions, sess.id)
	}
}

// resize applies a hot-reloaded max-sessions value and evicts if needed.
func (t *tracker) resize(maxSessions int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.maxSessions = max(maxSessions, 1)
	t.evictLocked()
}

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

// maxBaselineAge is the longest TTL any provider offers through this proxy (Claude 1h).
const maxBaselineAge = time.Hour

// export returns lanes touched within maxBaselineAge of now. Older lanes are
// useless after a restart anywhere: any request judged against them would be an
// idle-gap miss, which "cold" already conveys. This keeps the snapshot small
// (typically tens of lanes) while restarts during active work keep their verdicts.
func (t *tracker) export(now time.Time) []persistedLane {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []persistedLane
	for id, sess := range t.sessions {
		for i := range sess.lanes {
			l := &sess.lanes[i]
			if now.Sub(l.touchedAt) > maxBaselineAge {
				continue
			}
			data, err := l.print.MarshalBinary()
			if err != nil {
				continue
			}
			out = append(out, persistedLane{
				Session:      id,
				Model:        l.model,
				AuthID:       l.authID,
				TouchedAt:    l.touchedAt.UnixMilli(),
				CachedPrefix: l.cachedPrefix,
				TTLSec:       int64(l.ttl / time.Second),
				Print:        data,
			})
		}
	}
	return out
}

// restore loads persisted lanes, skipping malformed prints and lanes older than
// maxBaselineAge. Called once per snapshot path, before the worker delivers records.
func (t *tracker) restore(lanes []persistedLane, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, pl := range lanes {
		touched := time.UnixMilli(pl.TouchedAt)
		if pl.Session == "" || now.Sub(touched) > maxBaselineAge {
			continue
		}
		var p prompt.Print
		if err := p.UnmarshalBinary(pl.Print); err != nil {
			continue
		}
		sess := t.sessions[pl.Session]
		if sess == nil {
			sess = t.addLocked(pl.Session)
		}
		t.setLaneLocked(sess, lane{
			model:        pl.Model,
			authID:       pl.AuthID,
			touchedAt:    touched,
			cachedPrefix: pl.CachedPrefix,
			ttl:          time.Duration(pl.TTLSec) * time.Second,
			print:        &p,
		})
	}
	t.evictLocked()
}
