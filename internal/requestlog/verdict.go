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

func (s CauseSet) Has(c Cause) bool { return s&(1<<c) != 0 }
func (s *CauseSet) Add(c Cause)     { *s |= 1 << c }

// bodyCauses are the causes that name a place in the request body.
const bodyCauses CauseSet = 1<<CauseTools | 1<<CauseSystem | 1<<CauseSettings | 1<<CauseMessage | 1<<CauseInstructions | 1<<CauseTruncated

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

func packSegment(ref prompt.SegmentRef) packedSegment {
	return packedSegment(uint32(ref.Kind)<<24 | uint32(ref.Index)&0xFFFFFF)
}

func (p packedSegment) unpack() prompt.SegmentRef {
	return prompt.SegmentRef{Kind: prompt.SegmentKind(p >> 24), Index: int(p & 0xFFFFFF)}
}

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
// one shape. Fallback for an invalid breakdown: the explicit InputTokens,
// CacheReadTokens and CacheCreationTokens fields.
// Never use Detail.CachedTokens: parseClaudeUsageNode copies the creation count
// into it when the read count is 0, which would fake a cache read.
// known is false when every input and output bucket is zero.
func tokensFromDetail(d coreusage.Detail) (t tokenCounts, known bool) {
	if b := d.TokenBreakdown; b.Valid() {
		t = tokenCounts{
			uncached:  b.Input.UncachedTokens,
			read:      b.Input.CacheReadTokens,
			write:     b.Input.CacheWriteTokens,
			output:    b.Output.TotalTokens,
			reasoning: b.Output.ReasoningTokens,
		}
	} else {
		t = tokenCounts{
			uncached:  max(d.InputTokens, 0),
			read:      max(d.CacheReadTokens, 0),
			write:     max(d.CacheCreationTokens, 0),
			output:    max(d.OutputTokens, 0),
			reasoning: max(d.ReasoningTokens, 0),
		}
	}
	known = t.uncached != 0 || t.read != 0 || t.write != 0 || t.output != 0
	return t, known
}

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
	codexCacheStep       = 128
	// lookbackBlocks: Anthropic checks for prefix hits at most about 20 blocks
	// before each explicit breakpoint.
	lookbackBlocks = 20
	// codexIdleGuess: OpenAI evicts idle prefixes after roughly 5-10 minutes.
	codexIdleGuess = 5 * time.Minute
)

// cachedPrefix is what obs leaves in the cache for the next request.
// Claude reports reads and writes: everything up to the last breakpoint is
// cached after the request ran. Codex reports reads only: OpenAI caches the whole
// prompt prefix automatically in 128-token steps.
func cachedPrefix(obs observation) int64 {
	if obs.print.Format() == prompt.FormatCodexResponses {
		return obs.tokens.promptTotal() / codexCacheStep * codexCacheStep
	}
	return obs.tokens.read + obs.tokens.write
}

func toleranceFor(format prompt.Format, expected int64) int64 {
	if format == prompt.FormatCodexResponses {
		return max(codexToleranceTokens, int64(float64(expected)*codexToleranceRatio))
	}
	return max(claudeToleranceTokens, int64(float64(expected)*claudeToleranceRatio))
}

func notApplicable(reason NAReason) Verdict { return Verdict{Kind: VerdictNA, NA: reason} }

// judge computes the verdict for obs against the session's lanes and returns the
// lane update to apply (nil when the session baseline must not change).
// Pure function; called by the tracker with its mutex held.
// O(len(print.hashes)), no allocation on the hit path.
func judge(obs observation, sess *session) (Verdict, *laneUpdate) {
	switch {
	case obs.failed:
		return notApplicable(NAFailed), nil
	case obs.print == nil:
		return notApplicable(NAUnsupported), nil
	case !obs.tokensKnown:
		return notApplicable(NANoUsage), nil
	case obs.sessionID == "":
		return notApplicable(NANoSession), nil
	}
	base, sameModel := sess.baseline(obs.model)
	ttl, _ := obs.print.TTL()
	upd := &laneUpdate{model: obs.model, authID: obs.authID, touchedAt: obs.touchedAt, cachedPrefix: cachedPrefix(obs), ttl: ttl, print: obs.print}
	if base == nil {
		return Verdict{Kind: VerdictCold}, upd
	}
	if base.touchedAt.After(obs.requestedAt) {
		return notApplicable(NAConcurrent), nil
	}

	expected := base.cachedPrefix
	div := obs.print.Diverge(base.print)
	if div.Kind == prompt.Truncated {
		expected = min(expected, obs.tokens.promptTotal())
	}
	if expected < minCacheableTokens {
		return notApplicable(NABelowMinimum), upd
	}

	format := obs.print.Format()
	read := obs.tokens.read
	lost := max(int64(0), expected-read)
	v := Verdict{
		Expected: clampU32(expected),
		GapSec:   clampU32(int64(max(obs.requestedAt.Sub(base.touchedAt), 0) / time.Second)),
		TTLSec:   clampU32(int64(base.ttl / time.Second)),
	}
	switch {
	case lost <= toleranceFor(format, expected):
		v.Kind = VerdictHit
		return v, upd
	case read > 0:
		v.Kind = VerdictPartial
	default:
		v.Kind = VerdictMiss
	}

	var causes CauseSet
	if base.authID != obs.authID {
		causes.Add(CauseCredential)
	}
	if !sameModel {
		causes.Add(CauseModel)
	}
	switch div.Kind {
	case prompt.Changed:
		causes.Add(causeForSegment(div.At.Kind))
		v.Segment = packSegment(div.At)
		if div.At.Kind == prompt.SegmentTools {
			v.ToolsFrom, v.ToolsTo = clampU16(div.ToolsBefore), clampU16(div.ToolsAfter)
		}
	case prompt.Truncated:
		causes.Add(CauseTruncated)
		v.Segment = packSegment(div.At)
	}
	if div.CacheKeyChanged {
		causes.Add(CauseCacheKey)
	}
	if base.ttl > 0 && obs.requestedAt.Sub(base.touchedAt) > base.ttl {
		causes.Add(CauseIdle)
	}
	claude := format == prompt.FormatClaudeMessages
	if claude && obs.print.Breakpoints() == 0 {
		causes.Add(CauseNoBreakpoint)
	}
	if claude && v.Kind == VerdictPartial && div.Kind == prompt.Extends && div.AddedBlocks > lookbackBlocks {
		causes.Add(CauseLookback)
	}
	if div.VolatileChanged && causes&bodyCauses == 0 {
		causes.Add(CauseVolatile)
	}
	if causes == 0 {
		causes.Add(CauseUnexplained)
	}
	v.Causes = causes
	v.Primary, v.Confidence = pickPrimary(v.Kind, causes, format)
	return v, upd
}

// primaryOrder is the fixed precedence: the first present cause wins.
var primaryOrder = []Cause{
	CauseCredential, CauseModel, CauseIdle, CauseTools, CauseSystem, CauseSettings,
	CauseInstructions, CauseMessage, CauseTruncated, CauseCacheKey, CauseNoBreakpoint,
	CauseLookback, CauseVolatile, CauseUnexplained,
}

// pickPrimary chooses the single most likely cause.
//
// One exception to primaryOrder: when kind == Partial and a body cause is
// present, the body cause beats Idle. A partial read proves the cache was alive
// for the shared prefix, so the divergence point explains where reading stopped
// better than expiry does.
func pickPrimary(kind VerdictKind, causes CauseSet, format prompt.Format) (Cause, Confidence) {
	skipIdle := kind == VerdictPartial && causes&bodyCauses != 0
	for _, c := range primaryOrder {
		if !causes.Has(c) || (skipIdle && c == CauseIdle) {
			continue
		}
		return c, confidenceFor(c, format)
	}
	return CauseNone, ConfidenceNone
}

// confidenceFor is High for causes read from the body or the record, and Low
// for heuristics. Idle is High only on Claude, where the TTL is in the body.
func confidenceFor(c Cause, format prompt.Format) Confidence {
	switch c {
	case CauseCredential, CauseModel, CauseTools, CauseSystem, CauseSettings, CauseMessage, CauseInstructions, CauseTruncated:
		return ConfidenceHigh
	case CauseIdle:
		if format == prompt.FormatClaudeMessages {
			return ConfidenceHigh
		}
		return ConfidenceLow
	default:
		return ConfidenceLow
	}
}

// causeForSegment maps where the body diverged to a Cause.
func causeForSegment(k prompt.SegmentKind) Cause {
	switch k {
	case prompt.SegmentTools:
		return CauseTools
	case prompt.SegmentSystem:
		return CauseSystem
	case prompt.SegmentSettings:
		return CauseSettings
	case prompt.SegmentInstructions:
		return CauseInstructions
	default:
		return CauseMessage
	}
}

func clampU32(v int64) uint32 {
	if v < 0 {
		return 0
	}
	if v > 1<<32-1 {
		return 1<<32 - 1
	}
	return uint32(v)
}

func clampU16(v int) uint16 {
	if v < 0 {
		return 0
	}
	if v > 1<<16-1 {
		return 1<<16 - 1
	}
	return uint16(v)
}
