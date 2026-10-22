package requestlog

import (
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	log "github.com/sirupsen/logrus"
)

// snapshotFileName starts with "." and does not end in .json so the auth
// watcher and token stores never treat it as a credential.
const snapshotFileName = ".request-history.gz"

const (
	defaultMaxEntries    = 50_000
	maxMaxEntries        = 500_000
	defaultRetentionDays = 7
	maxRetentionDays     = 30
	defaultMaxSessions   = 1_000
	maxMaxSessions       = 10_000
	trackerHashBudget    = 1 << 20 // 8 MiB of uint64 segment hashes
	snapshotVersion      = 1
)

// row is the compact in-memory form of one entry. Value type in a ring slice:
// no per-row pointer, no per-row allocation except traceID (8 bytes) and, on
// failed rows only, errText.
//
// Size on amd64 (see README memory math): 160 bytes of 8-byte fields and string
// headers + 28 bytes of uint32 counters + 3 bytes status/flags + 28-byte Verdict
// = 224 bytes after alignment.
type row struct {
	seq       uint64
	atMs      int64    // RequestedAt, unix milliseconds
	requestID [16]byte // per-attempt uuid, parsed
	traceID   string   // per inbound request, shared by retries

	// interned: these repeat across thousands of rows and share one backing string.
	sessionID string
	keyHash   string // usagehistory.HashClientKey: 12 hex of sha256, "-" when no key
	authID    string
	provider  string
	model     string
	agent     string // user agent family, e.g. "claude-cli"

	errText string // failed rows only: first 200 bytes of Fail.Body; "" otherwise

	latencyMs uint32
	ttftMs    uint32
	uncached  uint32 // tokens; uint32 caps at 4.29e9 per request, far above any context window
	read      uint32
	write     uint32
	output    uint32
	reasoning uint32
	status    int16 // HTTP status: Fail.StatusCode when failed, else 200
	flags     rowFlags
	verdict   Verdict
}

type rowFlags uint8

const (
	rowFailed rowFlags = 1 << iota
	rowStream
)

// ring owns all entries. It is the single source of truth for both endpoints.
//
// Writers: the usage worker (append), the tick goroutine (prune, resize, load).
// Readers: HTTP handlers (query, sessions), the tick goroutine (snapshot copy).
// Lock: mu is an RWMutex because reads (UI polling, possibly several tabs) far
// outnumber the single writer, and a full 50k-row scan takes about 1 ms.
// fileMu serializes snapshot writes (tick flush vs Stop flush), as in quotaforecast.
type ring struct {
	mu      sync.RWMutex
	rows    []row // grows by append up to max, then overwritten circularly
	head    int   // index of the oldest row once full
	nextSeq uint64
	epoch   string            // random per store generation; changes when the file is missing or unreadable
	intern  map[string]string // rebuilt from live rows at prune time so it never grows without bound
	max     int
	dirty   atomic.Bool // set by append; cleared by copyForSave, which runs under the read lock

	fileMu sync.Mutex
}

func newRing(size int) *ring {
	return &ring{
		max:     max(size, 1),
		nextSeq: 1,
		epoch:   newEpoch(),
		intern:  make(map[string]string),
	}
}

// newEpoch returns a short random label for one store generation.
func newEpoch() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

// append assigns the next seq, interns strings, and stores r, overwriting the
// oldest row when full. O(1). Returns the assigned seq.
func (g *ring) append(r row) uint64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	r.seq = g.nextSeq
	g.nextSeq++
	g.internRowLocked(&r)
	if len(g.rows) < g.max {
		g.rows = append(g.rows, r)
	} else {
		g.rows[g.head] = r
		g.head = (g.head + 1) % len(g.rows)
	}
	g.dirty.Store(true)
	return r.seq
}

func (g *ring) internRowLocked(r *row) {
	r.sessionID = g.internLocked(r.sessionID)
	r.keyHash = g.internLocked(r.keyHash)
	r.authID = g.internLocked(r.authID)
	r.provider = g.internLocked(r.provider)
	r.model = g.internLocked(r.model)
	r.agent = g.internLocked(r.agent)
}

func (g *ring) internLocked(s string) string {
	if s == "" {
		return ""
	}
	if v, ok := g.intern[s]; ok {
		return v
	}
	g.intern[s] = s
	return s
}

// oldestSeqLocked is the seq of the oldest row held, or nextSeq when empty.
func (g *ring) oldestSeqLocked() uint64 { return g.nextSeq - uint64(len(g.rows)) }

// posLocked maps a held seq to its slice index.
func (g *ring) posLocked(seq uint64) int {
	return (g.head + int(seq-g.oldestSeqLocked())) % len(g.rows)
}

// at returns the row with sequence seq if still held.
// Rows are contiguous in seq, so position = (head + (seq - oldestSeq)) % len. O(1).
func (g *ring) at(seq uint64) (row, bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if len(g.rows) == 0 || seq < g.oldestSeqLocked() || seq >= g.nextSeq {
		return row{}, false
	}
	return g.rows[g.posLocked(seq)], true
}

// orderedLocked returns the live rows oldest-first in a new slice.
func (g *ring) orderedLocked() []row {
	out := make([]row, 0, len(g.rows))
	out = append(out, g.rows[g.head:]...)
	out = append(out, g.rows[:g.head]...)
	return out
}

// replaceLocked installs rows (oldest-first, at most max) and rebuilds the
// intern table from them.
func (g *ring) replaceLocked(rows []row) {
	if len(rows) > g.max {
		rows = rows[len(rows)-g.max:]
	}
	g.rows = make([]row, len(rows), min(g.max, max(len(rows), 1)))
	g.head = 0
	g.intern = make(map[string]string)
	for i := range rows {
		g.rows[i] = rows[i]
		g.internRowLocked(&g.rows[i])
	}
}

// prune drops rows older than cutoff from the oldest end (advancing head; rows
// are in publish order, which tracks RequestedAt within one request's latency),
// then rebuilds the intern table from live rows.
func (g *ring) prune(cutoff time.Time) {
	cutoffMs := cutoff.UnixMilli()
	g.mu.Lock()
	defer g.mu.Unlock()
	rows := g.orderedLocked()
	drop := 0
	for drop < len(rows) && rows[drop].atMs < cutoffMs {
		drop++
	}
	if drop == 0 {
		return
	}
	g.replaceLocked(rows[drop:])
	g.dirty.Store(true)
}

// resize applies a hot-reloaded max-entries: shrinking keeps the newest rows.
func (g *ring) resize(size int) {
	size = max(size, 1)
	g.mu.Lock()
	defer g.mu.Unlock()
	if size == g.max {
		return
	}
	rows := g.orderedLocked()
	g.max = size
	g.replaceLocked(rows)
}

// scan walks rows newest-first (asc=false) or oldest-first between the seq bounds
// under RLock, calling keep for each; stops when keep returns false.
// The live-refresh path ("since" cursor) touches only rows newer than the cursor,
// so a 5-second poll with no new traffic costs one RLock and zero row visits.
func (g *ring) scan(fromSeq, toSeq uint64, asc bool, keep func(*row) bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if len(g.rows) == 0 {
		return
	}
	lo := max(fromSeq, g.oldestSeqLocked())
	hi := min(toSeq, g.nextSeq-1)
	if lo > hi {
		return
	}
	if asc {
		for seq := lo; seq <= hi; seq++ {
			if !keep(&g.rows[g.posLocked(seq)]) {
				return
			}
		}
		return
	}
	for seq := hi; seq >= lo; seq-- {
		if !keep(&g.rows[g.posLocked(seq)]) {
			return
		}
		if seq == 0 {
			return
		}
	}
}

// bounds returns the oldest and newest seq held and the epoch.
func (g *ring) bounds() (oldest, newest uint64, epoch string) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if len(g.rows) == 0 {
		return 0, 0, g.epoch
	}
	return g.oldestSeqLocked(), g.nextSeq - 1, g.epoch
}

// --- snapshot ---

// snapshotV1 is the gzip JSON file. Prints appear only in Lanes.
type snapshotV1 struct {
	Version int             `json:"v"`
	SavedAt int64           `json:"saved_at"`
	Epoch   string          `json:"epoch"`
	NextSeq uint64          `json:"next_seq"`
	Rows    []persistedRow  `json:"rows"`
	Lanes   []persistedLane `json:"lanes"`
}

// persistedRow mirrors row with short JSON keys. Interned strings are written in
// full; gzip removes the repetition.
type persistedRow struct {
	Seq       uint64     `json:"q"`
	AtMs      int64      `json:"t"`
	RequestID string     `json:"r"`
	TraceID   string     `json:"tr,omitempty"`
	SessionID string     `json:"s,omitempty"`
	KeyHash   string     `json:"k,omitempty"`
	AuthID    string     `json:"a,omitempty"`
	Provider  string     `json:"p,omitempty"`
	Model     string     `json:"m,omitempty"`
	Agent     string     `json:"g,omitempty"`
	ErrText   string     `json:"e,omitempty"`
	LatencyMs uint32     `json:"l,omitempty"`
	TTFTMs    uint32     `json:"f,omitempty"`
	Tokens    [5]uint32  `json:"n"` // uncached, read, write, output, reasoning
	Status    int16      `json:"st"`
	Flags     uint8      `json:"fl,omitempty"`
	Verdict   [11]uint32 `json:"v"` // Verdict fields in declaration order
}

func (r *row) persisted() persistedRow {
	v := r.verdict
	return persistedRow{
		Seq:       r.seq,
		AtMs:      r.atMs,
		RequestID: uuid.UUID(r.requestID).String(),
		TraceID:   r.traceID,
		SessionID: r.sessionID,
		KeyHash:   r.keyHash,
		AuthID:    r.authID,
		Provider:  r.provider,
		Model:     r.model,
		Agent:     r.agent,
		ErrText:   r.errText,
		LatencyMs: r.latencyMs,
		TTFTMs:    r.ttftMs,
		Tokens:    [5]uint32{r.uncached, r.read, r.write, r.output, r.reasoning},
		Status:    r.status,
		Flags:     uint8(r.flags),
		Verdict: [11]uint32{uint32(v.Kind), uint32(v.NA), uint32(v.Primary), uint32(v.Confidence), uint32(v.Causes),
			uint32(v.Segment), uint32(v.ToolsFrom), uint32(v.ToolsTo), v.Expected, v.GapSec, v.TTLSec},
	}
}

func (p *persistedRow) row() row {
	v := p.Verdict
	r := row{
		seq:       p.Seq,
		atMs:      p.AtMs,
		traceID:   p.TraceID,
		sessionID: p.SessionID,
		keyHash:   p.KeyHash,
		authID:    p.AuthID,
		provider:  p.Provider,
		model:     p.Model,
		agent:     p.Agent,
		errText:   p.ErrText,
		latencyMs: p.LatencyMs,
		ttftMs:    p.TTFTMs,
		uncached:  p.Tokens[0],
		read:      p.Tokens[1],
		write:     p.Tokens[2],
		output:    p.Tokens[3],
		reasoning: p.Tokens[4],
		status:    p.Status,
		flags:     rowFlags(p.Flags),
		verdict: Verdict{Kind: VerdictKind(v[0]), NA: NAReason(v[1]), Primary: Cause(v[2]), Confidence: Confidence(v[3]),
			Causes: CauseSet(v[4]), Segment: packedSegment(v[5]), ToolsFrom: uint16(v[6]), ToolsTo: uint16(v[7]),
			Expected: v[8], GapSec: v[9], TTLSec: v[10]},
	}
	if id, err := uuid.Parse(p.RequestID); err == nil {
		r.requestID = id
	}
	return r
}

// copyForSave copies rows under RLock (memcpy of the value slice; strings are
// immutable and shared) and clears dirty. Encoding and gzip run outside the lock,
// so the usage worker never waits on disk or compression.
func (g *ring) copyForSave() (rows []row, nextSeq uint64, epoch string, dirty bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if !g.dirty.Swap(false) {
		return nil, g.nextSeq, g.epoch, false
	}
	return g.orderedLocked(), g.nextSeq, g.epoch, true
}

// buildSnapshot assembles the file form of a save.
func buildSnapshot(rows []row, nextSeq uint64, epoch string, lanes []persistedLane, now time.Time) *snapshotV1 {
	data := &snapshotV1{Version: snapshotVersion, SavedAt: now.Unix(), Epoch: epoch, NextSeq: nextSeq, Rows: make([]persistedRow, len(rows)), Lanes: lanes}
	for i := range rows {
		data.Rows[i] = rows[i].persisted()
	}
	return data
}

// load replaces the ring from path. A missing file keeps the fresh epoch; an
// unreadable file returns an error and keeps the fresh epoch (the UI then drops
// its cursor). Returns the persisted lanes for tracker.restore.
func (g *ring) load(path string) ([]persistedLane, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open request history: %w", err)
	}
	defer func() {
		if errClose := f.Close(); errClose != nil {
			log.WithError(errClose).Debug("failed to close request history file")
		}
	}()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("read request history: %w", err)
	}
	var data snapshotV1
	if err = json.NewDecoder(gz).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode request history: %w", err)
	}
	if data.Version != snapshotVersion || data.Epoch == "" {
		return nil, fmt.Errorf("unsupported request history version %d", data.Version)
	}
	// Rows must be contiguous in seq for at() and scan() to index by arithmetic.
	rows := make([]row, 0, len(data.Rows))
	nextSeq := max(data.NextSeq, 1)
	for i := range data.Rows {
		r := data.Rows[i].row()
		if r.seq == 0 || (len(rows) > 0 && r.seq != rows[len(rows)-1].seq+1) {
			return nil, errors.New("request history rows are not contiguous")
		}
		rows = append(rows, r)
		nextSeq = r.seq + 1
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.epoch = data.Epoch
	g.nextSeq = nextSeq
	g.replaceLocked(rows)
	g.dirty.Store(false)
	return data.Lanes, nil
}

// writeSnapshot writes data atomically: temp file in the same dir, gzip JSON,
// close, rename. Same shape as quotaforecast.writeHistoryFile.
func writeSnapshot(mu *sync.Mutex, path string, data *snapshotV1) error {
	mu.Lock()
	defer mu.Unlock()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create request history dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, snapshotFileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("create request history temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		if errRemove := os.Remove(tmpPath); errRemove != nil && !errors.Is(errRemove, os.ErrNotExist) {
			log.WithError(errRemove).Debug("failed to remove request history temp file")
		}
	}
	gz := gzip.NewWriter(tmp)
	if err = json.NewEncoder(gz).Encode(data); err != nil {
		_ = gz.Close()
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("encode request history: %w", err)
	}
	if err = gz.Close(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("compress request history: %w", err)
	}
	if err = tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close request history temp file: %w", err)
	}
	if err = os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("replace request history: %w", err)
	}
	return nil
}
