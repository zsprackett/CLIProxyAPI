package requestlog

import (
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/requestlog/prompt"
	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

// VerdictKind is the cache outcome of one upstream attempt, judged against the
// previous successful attempt of the same session (see judge).
type VerdictKind uint8

const (
	VerdictNA      VerdictKind = iota // not judged; NAReason says why
	VerdictCold                       // no baseline in this session (first request, or baseline evicted/lost)
	VerdictHit                        // read >= expected - tolerance
	VerdictPartial                    // 0 < read < expected - tolerance
	VerdictMiss                       // read == 0 while expected >= minimum cacheable
)

// NAReason explains a VerdictNA.
type NAReason uint8

const (
	NANone         NAReason = iota
	NAFailed                // the attempt failed; no usage, never a baseline
	NANoUsage               // success but the upstream reported no token usage
	NANoSession             // record has no SessionID
	NAUnsupported           // provider/executor not fingerprinted (no Print)
	NABelowMinimum          // expected cacheable prefix below the provider minimum
	NAConcurrent            // baseline is newer than this request (parallel requests in one session)
)

// Cause is one reason a request read less cache than expected.
type Cause uint8

const (
	CauseNone         Cause = iota
	CauseCredential         // auth changed: Anthropic/OpenAI caches are per organization/account
	CauseModel              // no same-model baseline in this session; judged against the session's last model
	CauseTools              // tools array changed (Divergence.At.Kind == SegmentTools)
	CauseSystem             // system block i changed
	CauseSettings           // tool_choice or thinking changed (Claude: invalidates the message cache)
	CauseMessage            // message i changed (Claude) / input item i changed (Codex)
	CauseInstructions       // Codex instructions changed
	CauseTruncated          // history is shorter than the baseline's (rewind, edit, retry of an earlier turn)
	CauseIdle               // gap since the baseline touched the cache exceeds the TTL
	CauseCacheKey           // Codex prompt_cache_key changed (different routing shard)
	CauseLookback           // Claude: > 20 content blocks added since the last breakpoint
	CauseNoBreakpoint       // Claude: the request carried no cache_control at all
	CauseVolatile           // only the excluded billing-header block changed
	CauseUnexplained        // nothing above applies (Codex: best-effort cache routing)
)

// CauseSet is a bitset of every cause detected, so the UI can show "also changed".
type CauseSet uint16

func (s CauseSet) Has(c Cause) bool { panic("not implemented") }
func (s *CauseSet) Add(c Cause)     { panic("not implemented") }

// Confidence says whether the primary cause is deterministic.
type Confidence uint8

const (
	ConfidenceNone Confidence = iota
	ConfidenceHigh            // credential, model, body divergence, truncation, idle beyond a known (Claude) TTL
	ConfidenceLow             // Codex idle heuristic, cache key, lookback, volatile, unexplained
)

// Verdict is stored in every entry (28 bytes). It is the ONLY cache-diagnosis
// state kept per entry: prints live only in the session tracker, one per
// (session, model) lane.
type Verdict struct {
	Kind       VerdictKind
	NA         NAReason
	Primary    Cause
	Confidence Confidence
	Causes     CauseSet
	Segment    packedSegment // where the body diverged, when a body cause is present
	ToolsFrom  uint16        // with CauseTools: tool count before -> after
	ToolsTo    uint16
	Expected   uint32 // expected cache read in tokens (0 when not judged)
	GapSec     uint32 // seconds since the baseline last touched the cache
	TTLSec     uint32 // TTL the gap was judged against (0 when unknown)
}

// packedSegment is prompt.SegmentRef in 4 bytes: kind in the top byte, index below.
type packedSegment uint32

func packSegment(ref prompt.SegmentRef) packedSegment { panic("not implemented") }
func (p packedSegment) unpack() prompt.SegmentRef     { panic("not implemented") }

// observation is what judge needs from one record. Built by HandleUsage.
type observation struct {
	sessionID   string
	model       string
	authID      string
	requestedAt time.Time
	touchedAt   time.Time // requestedAt + TTFT (or + latency): when the prompt was processed
	failed      bool
	tokens      tokenCounts
	tokensKnown bool
	print       *prompt.Print // nil when not fingerprinted
}

// tokenCounts are mutually exclusive input buckets plus output.
type tokenCounts struct {
	uncached, read, write, output, reasoning int64
}

func (t tokenCounts) promptTotal() int64 { return t.uncached + t.read + t.write }

// tokensFromDetail takes buckets from Detail.TokenBreakdown when Valid(), which
// normalizes Claude (independent buckets) and Codex (cache included in input) to
// one shape. Fallback for an invalid breakdown: InputTokens, CacheReadTokens and
// CacheCreationTokens with the provider's semantics.
// Never use Detail.CachedTokens for Claude: parseClaudeUsageNode copies the
// creation count into it when the read count is 0.
// known is false when every bucket is zero.
func tokensFromDetail(d coreusage.Detail) (t tokenCounts, known bool) { panic("not implemented") }

// Thresholds. Named constants so tests and the README use the same numbers.
const (
	// minCacheableTokens: Anthropic's smallest minimum (1024; some models need
	// 2048 or 4096) and OpenAI's automatic caching floor (1024). An expected
	// prefix below this cannot be read, so the verdict is n/a, not a miss.
	minCacheableTokens = 1024
	// Claude tolerance: tokenization of an identical prefix is identical, so a hit
	// should read exactly the expected count. The slack absorbs a breakpoint that
	// lands one block earlier than the previous request's.
	claudeToleranceTokens = 256
	claudeToleranceRatio  = 0.02
	// Codex tolerance: cached_tokens moves in 128-token steps and the baseline's
	// last partial chunk is never cached.
	codexToleranceTokens = 256
	codexToleranceRatio  = 0.05
	// lookbackBlocks: Anthropic checks for prefix hits at most about 20 blocks
	// before each explicit breakpoint.
	lookbackBlocks = 20
	// codexIdleGuess: OpenAI evicts idle prefixes after roughly 5-10 minutes.
	codexIdleGuess = 5 * time.Minute
)

// judge computes the verdict for obs against the session's lanes and returns the
// lane update to apply (nil when the session baseline must not change).
// Pure function; called by the tracker with its mutex held.
// O(len(print.hashes)), no allocation on the hit path.
//
//	switch {
//	case obs.failed:            return NA(NAFailed), nil
//	case obs.print == nil:      return NA(NAUnsupported), nil
//	case !obs.tokensKnown:      return NA(NANoUsage), nil
//	case obs.sessionID == "":   return NA(NANoSession), nil
//	}
//	base, sameModel := sess.baseline(obs.model)   // same-model lane, else most recently touched lane
//	upd := &laneUpdate{obs.model, obs.authID, obs.touchedAt, cachedPrefix(obs), ttl(obs.print), obs.print}
//	if base == nil:                           return Cold, upd
//	if base.touchedAt.After(obs.requestedAt): return NA(NAConcurrent), nil
//
//	// Expected read E: what the baseline left in the cache.
//	//   Claude (read and write known): E = base.read + base.write
//	//       Everything up to the baseline's last breakpoint is cached after it ran,
//	//       whether it was read or freshly written.
//	//   Codex (read only):             E = floor(base.promptTotal / 128) * 128
//	//       OpenAI caches the whole prompt prefix automatically in 128-token steps.
//	E := base.cachedPrefix
//	div := obs.print.Diverge(base.print)
//	if div.Kind == Truncated: E = min(E, obs.tokens.promptTotal())
//	if E < minCacheableTokens: return NA(NABelowMinimum), upd
//
//	R    := obs.tokens.read
//	tol  := max(tolTokens, ratio*E)          // per format, constants above
//	lost := max(0, E - R)
//	kind := Hit if lost <= tol; else Partial if R > 0; else Miss
//	if kind == Hit: return Verdict{Kind: Hit, Expected: E, GapSec, TTLSec}, upd
//
//	// Collect every cause, then pick one primary.
//	if base.authID != obs.authID:            causes.Add(Credential)
//	if !sameModel:                           causes.Add(Model)
//	switch div.Kind {
//	case Changed:   causes.Add(causeForSegment(div.At.Kind)); v.Segment = div.At
//	case Truncated: causes.Add(Truncated);                    v.Segment = div.At
//	}
//	if div.CacheKeyChanged:                  causes.Add(CacheKey)
//	ttl := base.ttl   // Claude: from the baseline print; Codex: codexIdleGuess
//	gap := obs.requestedAt - base.touchedAt
//	if ttl > 0 && gap > ttl:                 causes.Add(Idle)
//	if Claude && obs.print.Breakpoints()==0: causes.Add(NoBreakpoint)
//	if Claude && kind == Partial && div.Kind == Extends && div.AddedBlocks > lookbackBlocks:
//	                                         causes.Add(Lookback)
//	if div.VolatileChanged && no body cause: causes.Add(Volatile)
//	if causes == 0:                          causes.Add(Unexplained)
//	v.Primary, v.Confidence = pickPrimary(kind, causes, format)
//	return v, upd
func judge(obs observation, sess *session) (Verdict, *laneUpdate) { panic("not implemented") }

// pickPrimary chooses the single most likely cause.
//
// Fixed precedence (first present wins):
//
//	Credential > Model > Idle > Tools > System > Settings > Instructions >
//	Message > Truncated > CacheKey > NoBreakpoint > Lookback > Volatile > Unexplained
//
// One exception: when kind == Partial and a body cause (Tools..Truncated) is
// present, the body cause beats Idle. A partial read proves the cache was alive
// for the shared prefix, so the divergence point explains where reading stopped
// better than expiry does.
//
// Confidence: High for Credential, Model, body causes and Truncated, and for Idle
// on Claude (the TTL is in the body). Low for Idle on Codex, CacheKey, Lookback,
// Volatile, NoBreakpoint and Unexplained.
func pickPrimary(kind VerdictKind, causes CauseSet, format prompt.Format) (Cause, Confidence) {
	panic("not implemented")
}

// causeForSegment maps where the body diverged to a Cause.
func causeForSegment(k prompt.SegmentKind) Cause { panic("not implemented") }
