package usagehistory

import (
	"context"
	"strings"
	"sync"
	"time"

	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
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
func (c Counters) Add(o Counters) Counters {
	return Counters{
		Requests:     c.Requests + o.Requests,
		Failures:     c.Failures + o.Failures,
		Tokens:       c.Tokens.Add(o.Tokens),
		LatencyMsSum: c.LatencyMsSum + o.LatencyMsSum,
	}
}

// rollupOf reduces one usage record to its key and counters. Pure apart from
// reading the client User-Agent from ctx (values stay readable after cancel).
// ok=false means "do not record": Generate=false token-count probes are not billed.
func rollupOf(ctx context.Context, rec coreusage.Record, now time.Time) (Key, Counters, bool) {
	if !coreusage.GenerateEnabled(rec.Generate) {
		return Key{}, Counters{}, false
	}
	at := rec.RequestedAt
	if at.IsZero() {
		at = now
	}
	detail := rec.Detail
	if !detail.TokenBreakdown.Valid() {
		detail = coreusage.EnsureTokenBreakdownForProvider(detail, rec.Provider, rec.ExecutorType)
	}
	b := detail.TokenBreakdown
	var t Tokens
	if b.Valid() {
		t = Tokens{
			Uncached:     b.Input.UncachedTokens,
			CacheRead:    b.Input.CacheReadTokens,
			CacheWrite5m: b.Input.CacheWriteTokens - b.Input.CacheWrite1hTokens, // Valid() keeps this >= 0
			CacheWrite1h: b.Input.CacheWrite1hTokens,
			Output:       b.Output.NonReasoningTokens,
			Reasoning:    b.Output.ReasoningTokens,
			Unclassified: b.UnclassifiedTokens,
		}
	} else {
		t = Tokens{Unclassified: max(detail.TotalTokens, 0)}
	}
	model := strings.TrimSpace(rec.Model)
	if model == "" {
		model = strings.TrimSpace(rec.Alias)
	}
	key := Key{
		Hour:     hourStart(at),
		Provider: dim(strings.ToLower(strings.TrimSpace(rec.Provider)), "unknown"),
		AuthID:   dim(strings.TrimSpace(rec.AuthID), "-"),
		Model:    dim(model, "unknown"),
		KeyHash:  HashClientKey(rec.APIKey),
		Agent:    AgentFamily(internallogging.GetClientRequestMetadata(ctx).UserAgent),
		LongCtx:  b.Valid() && b.Input.TotalTokens > LongContextThreshold,
	}
	c := Counters{Requests: 1, Tokens: t, LatencyMsSum: max(rec.Latency.Milliseconds(), 0)}
	if rec.Failed {
		c.Failures = 1
	}
	return key, c, true
}

// dim caps a dimension value and replaces an empty one with fallback.
func dim(s, fallback string) string {
	if s == "" {
		return fallback
	}
	if len(s) > maxDimLen {
		return s[:maxDimLen]
	}
	return s
}

// hourStart truncates to the UTC hour (unix seconds).
func hourStart(t time.Time) int64 { return t.UTC().Truncate(time.Hour).Unix() }

// pending is the in-memory rollup since the last flush. HandleUsage (usage worker
// goroutine) adds; the run loop swaps it out and, if the write fails, restores it.
// Nothing else touches it.
type pending struct {
	mu      sync.Mutex
	cells   map[Key]Counters
	limit   int   // maxPendingKeys; tests lower it
	dropped int64 // records refused because limit was reached; reported by swap
}

func newPending() *pending { return &pending{cells: make(map[Key]Counters), limit: maxPendingKeys} }

// add merges c into k. No I/O; an existing key is always merged.
func (p *pending) add(k Key, c Counters) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mergeLocked(k, c)
}

func (p *pending) mergeLocked(k Key, c Counters) {
	if cur, ok := p.cells[k]; ok {
		p.cells[k] = cur.Add(c)
		return
	}
	if len(p.cells) >= p.limit {
		p.dropped += c.Requests
		return
	}
	p.cells[k] = c
}

// swap returns the current cells and the drop count, and installs an empty map.
func (p *pending) swap() (map[Key]Counters, int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	cells, dropped := p.cells, p.dropped
	p.cells, p.dropped = make(map[Key]Counters), 0
	return cells, dropped
}

// restore merges a batch whose write failed back in (additive, so ordering with
// newer adds does not matter). Respects the key limit like add.
func (p *pending) restore(batch map[Key]Counters) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for k, c := range batch {
		p.mergeLocked(k, c)
	}
}

// discard empties pending (enabled -> disabled while the DB was broken).
func (p *pending) discard() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cells, p.dropped = make(map[Key]Counters), 0
}
