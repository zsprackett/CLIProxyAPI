package usagehistory

import (
	"context"
	"sync"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

// LongContextThreshold is the prompt size (uncached + cache read + cache write)
// above which providers bill a premium tier (LiteLLM *_above_200k_tokens fields:
// Claude 1M-context, Gemini Pro). The rollup keeps it as a key column because a
// tier cannot be recovered from an aggregate later.
const LongContextThreshold = 200_000

// maxPendingKeys bounds memory while the DB is unavailable (phaseBroken). ~50
// keys/day are measured, so this is years of outage before anything drops.
const maxPendingKeys = 100_000

// maxDimLen caps every string dimension so a hostile alias cannot bloat a row.
const maxDimLen = 128

// Key is one rollup row's identity. Every field is non-empty ("-" or "unknown"
// for missing values), so the SQL primary key never holds NULL or "".
type Key struct {
	Hour     int64  // unix seconds, UTC hour start of RequestedAt (attempt start)
	Provider string // lower-case executor id
	AuthID   string // "-" when empty
	Model    string // Record.Model, else Alias, else "unknown"
	KeyHash  string // HashClientKey(Record.APIKey)
	Agent    string // AgentFamily(User-Agent)
	LongCtx  bool   // prompt > LongContextThreshold
}

// Counters are additive: merging two Counters is field-wise +. The upsert relies
// on that (ON CONFLICT ... SET x = x + excluded.x).
type Counters struct {
	Requests     int64
	Failures     int64
	Tokens       Tokens
	LatencyMsSum int64
}

// Add returns c + o.
func (c Counters) Add(o Counters) Counters { panic("not implemented") }

// rollupOf reduces one usage record to its key and counters. Pure apart from
// reading the client User-Agent from ctx (values stay readable after cancel).
// ok=false means "do not record" (Generate=false: token-count probes are not billed).
//
// pseudocode:
//
//	if !coreusage.GenerateEnabled(rec.Generate) { return ok=false }
//	at := rec.RequestedAt; if zero { at = now }
//	detail := rec.Detail
//	if !detail.TokenBreakdown.Valid() {
//	    detail = coreusage.EnsureTokenBreakdownForProvider(detail, rec.Provider, rec.ExecutorType)
//	}
//	b := detail.TokenBreakdown
//	var t Tokens
//	if b.Valid() {
//	    t = Tokens{
//	        Uncached:     b.Input.UncachedTokens,
//	        CacheRead:    b.Input.CacheReadTokens,
//	        CacheWrite1h: b.Input.CacheWrite1hTokens,
//	        CacheWrite5m: b.Input.CacheWriteTokens - b.Input.CacheWrite1hTokens, // Valid() guarantees >= 0
//	        Output:       b.Output.NonReasoningTokens,
//	        Reasoning:    b.Output.ReasoningTokens,
//	        Unclassified: b.UnclassifiedTokens,
//	    }
//	} else {
//	    t = Tokens{Unclassified: max(detail.TotalTokens, 0)}   // never Detail.CachedTokens
//	}
//	key := Key{
//	    Hour:     at.UTC().Truncate(time.Hour).Unix(),
//	    Provider: capLen(lower(trim(rec.Provider)) or "unknown"),
//	    AuthID:   capLen(trim(rec.AuthID) or "-"),
//	    Model:    capLen(trim(rec.Model) or trim(rec.Alias) or "unknown"),
//	    KeyHash:  HashClientKey(rec.APIKey),
//	    Agent:    AgentFamily(internallogging.GetClientRequestMetadata(ctx).UserAgent),
//	    LongCtx:  b.Input.TotalTokens > LongContextThreshold,
//	}
//	c := Counters{Requests: 1, Tokens: t, LatencyMsSum: max(rec.Latency.Milliseconds(), 0)}
//	if rec.Failed { c.Failures = 1 }
func rollupOf(ctx context.Context, rec coreusage.Record, now time.Time) (Key, Counters, bool) {
	panic("not implemented")
}

// pending is the in-memory rollup since the last flush. HandleUsage (usage worker
// goroutine) adds; the run loop swaps it out and, if the write fails, restores it.
// Nothing else touches it.
type pending struct {
	mu      sync.Mutex
	cells   map[Key]Counters
	dropped int64 // records refused because maxPendingKeys was reached; logged and reset by the loop
}

func newPending() *pending { return &pending{cells: make(map[Key]Counters)} }

// add merges c into k. O(1), no allocation for an existing key, no I/O.
func (p *pending) add(k Key, c Counters) { panic("not implemented") }

// swap returns the current cells and installs an empty map.
func (p *pending) swap() (map[Key]Counters, int64) { panic("not implemented") }

// restore merges a batch whose write failed back in (additive, so ordering with
// newer adds does not matter). Respects maxPendingKeys like add.
func (p *pending) restore(batch map[Key]Counters) { panic("not implemented") }

// discard empties pending (enabled -> disabled while the DB was broken).
func (p *pending) discard() { panic("not implemented") }
