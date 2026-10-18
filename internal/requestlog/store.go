package requestlog

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"sync"
	"time"
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
	keyHash   string // quotaforecast.HashClientKey: 12 hex of sha256, "-" when no key
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
	dirty   bool

	fileMu sync.Mutex
}

func newRing(size int) *ring {
	return &ring{
		max:    max(size, 1),
		epoch:  newEpoch(),
		intern: make(map[string]string),
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
func (g *ring) append(r row) uint64 { panic("not implemented") }

// at returns the row with sequence seq if still held.
// Rows are contiguous in seq, so position = (head + (seq - oldestSeq)) % len. O(1).
func (g *ring) at(seq uint64) (row, bool) { panic("not implemented") }

// prune drops rows older than cutoff from the oldest end (advancing head; rows
// are in publish order, which tracks RequestedAt within one request's latency),
// then rebuilds the intern table from live rows.
func (g *ring) prune(cutoff time.Time) { panic("not implemented") }

// resize applies a hot-reloaded max-entries: shrinking keeps the newest rows.
func (g *ring) resize(max int) { panic("not implemented") }

// scan walks rows newest-first (asc=false) or oldest-first between the seq bounds
// under RLock, calling keep for each; stops when keep returns false.
// The live-refresh path ("since" cursor) touches only rows newer than the cursor,
// so a 5-second poll with no new traffic costs one RLock and zero row visits.
func (g *ring) scan(fromSeq, toSeq uint64, asc bool, keep func(*row) bool) { panic("not implemented") }

// bounds returns the oldest and newest seq held and the epoch.
func (g *ring) bounds() (oldest, newest uint64, epoch string) { panic("not implemented") }

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

// copyForSave copies rows under RLock (memcpy of the value slice; strings are
// immutable and shared) and clears dirty. Encoding and gzip run outside the lock,
// so the usage worker never waits on disk or compression.
func (g *ring) copyForSave() (rows []row, nextSeq uint64, epoch string, dirty bool) {
	panic("not implemented")
}

// load replaces the ring from path. A missing file starts a new epoch; an
// unreadable file is logged and starts a new epoch (the UI then drops its cursor).
// Returns the persisted lanes for tracker.restore.
func (g *ring) load(path string) ([]persistedLane, error) { panic("not implemented") }

// writeSnapshot writes data atomically: temp file in the same dir, gzip JSON,
// close, rename. Same shape as quotaforecast.writeHistoryFile.
func writeSnapshot(mu *sync.Mutex, path string, data *snapshotV1) error { panic("not implemented") }
