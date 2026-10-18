// Package prompt turns a final upstream request body into a Print: an ordered
// list of 64-bit hashes, one per cache-prefix segment (tools, each system block,
// request settings, each message or input item). A Print never holds prompt text.
//
// It is a leaf package (imports only gjson, crypto/sha256, context) so that
// internal/runtime/executor/helps can import it without pulling in the request
// log store, config, or the auth manager.
//
// The Print travels from the UsageReporter to the request-log plugin as a
// context value attached at publish time (see WithPrint). It is NOT a field of
// usage.Record, so no other usage plugin, the plugin host adapters, or the Redis
// queue ever see it, and side-model records from PublishAdditionalModel never
// carry it.
package prompt

import (
	"context"
	"sync/atomic"
	"time"
)

// Format names the upstream wire protocol a body was written in. Only formats
// with a known prefix-cache layout are fingerprinted; everything else yields a
// nil Print and the entry gets verdict n/a ("unsupported").
type Format uint8

const (
	FormatUnknown        Format = iota
	FormatClaudeMessages        // Anthropic Messages: tools -> system -> settings -> messages
	FormatCodexResponses        // OpenAI Responses (Codex HTTP + websocket): tools -> instructions -> input
)

// ParseFormat maps the executor's translator format string ("claude", "codex")
// to a Format. Unknown strings return FormatUnknown.
func ParseFormat(s string) Format { panic("not implemented") }

// SegmentKind names one cache-prefix segment.
type SegmentKind uint8

const (
	SegmentNone         SegmentKind = iota
	SegmentTools                    // the whole tools array, one hash
	SegmentSystem                   // Claude system block i (billing header excluded)
	SegmentSettings                 // Claude tool_choice + thinking: invalidates the message cache only
	SegmentMessage                  // Claude messages[i]
	SegmentInstructions             // Codex instructions string
	SegmentInput                    // Codex input[i]
)

// SegmentRef locates a segment in the CURRENT request's layout.
type SegmentRef struct {
	Kind  SegmentKind
	Index int // index within its kind (system block i, message i); 0 for tools/settings/instructions
}

// TTLClass is the longest cache TTL requested by any breakpoint in the body.
type TTLClass uint8

const (
	TTLNone    TTLClass = iota // no cache_control breakpoint at all (Claude) / not applicable
	TTL5m                      // Anthropic default ephemeral TTL
	TTL1h                      // cache_control.ttl == "1h"
	TTLUnknown                 // Codex: provider-managed, roughly 5-10 min idle, not reported
)

// Print is the cache-relevant shape of one upstream request body.
// All fields are unexported: the request log stores, compares and persists Prints
// but never looks inside them.
type Print struct {
	format Format

	// hashes is the flat, ordered prefix: [tools?][system...][settings?][items...]
	// where items are Claude messages or Codex input items. Each hash is the first
	// 8 bytes of sha256 over the segment's canonical bytes (see canonical rules in
	// claude.go / codex.go). Capped at MaxSegments; beyond the cap the tail is
	// folded into one final hash and truncatedTail is set.
	hashes []uint64
	layout layout

	toolCount     uint16 // number of tools, for "tools changed (37 -> 38)"
	blocks        uint32 // Claude: total content blocks across messages (lookback rule)
	breakpoints   uint8  // Claude: number of cache_control markers found
	ttl           TTLClass
	volatile      uint64 // hash of excluded volatile blocks (Claude billing header); 0 if none
	cacheKey      uint64 // Codex: hash of prompt_cache_key; 0 if absent
	incremental   bool   // Codex websocket: previous_response_id present, input is a delta
	truncatedTail bool   // more than MaxSegments segments; tail folded
}

// layout records how many hashes of each kind the flat list holds, in order.
type layout struct {
	tools    uint8  // 0 or 1
	system   uint16 // Claude system blocks kept (billing header excluded); Codex: 0
	settings uint8  // 0 or 1 (Claude only)
	instr    uint8  // 0 or 1 (Codex only)
	items    uint32 // messages (Claude) or input items (Codex)
}

// MaxSegments bounds one Print to 4096 hashes (32 KiB). Claude Code sessions are
// compacted long before this; the cap only guards pathological clients.
const MaxSegments = 4096

// Of fingerprints body. It returns nil for FormatUnknown, an invalid body, or
// when capture is disabled. It never returns an error: a fingerprint is
// diagnostics, never a reason to fail a request.
//
// Cost: one pass over body with gjson plus sha256 over each segment's bytes,
// about 1 ms per MB. Called by UsageReporter at publish time, after the upstream
// response is fully consumed, never before the upstream request is sent.
func Of(body []byte, format Format) *Print {
	// switch format:
	//   FormatClaudeMessages: return claudePrint(body)
	//   FormatCodexResponses: return codexPrint(body)
	//   default: return nil
	panic("not implemented")
}

// Size returns the number of hashes held, used by the session tracker's hash budget.
func (p *Print) Size() int { panic("not implemented") }

// Format returns the wire format the print was computed from.
func (p *Print) Format() Format { panic("not implemented") }

// TTL returns the longest requested cache TTL as a duration:
// TTL5m -> 5m, TTL1h -> 1h, TTLUnknown -> 5m (heuristic, low confidence), TTLNone -> 0.
func (p *Print) TTL() (time.Duration, TTLClass) { panic("not implemented") }

// Breakpoints returns the number of cache_control markers (Claude only).
func (p *Print) Breakpoints() int { panic("not implemented") }

// DivergenceKind classifies how the current print relates to the previous one.
type DivergenceKind uint8

const (
	// Extends: prev is a strict prefix of cur (normal append-only turn), or equal.
	Extends DivergenceKind = iota
	// Changed: some segment inside prev's length differs. At names the first one.
	Changed
	// Truncated: cur is a strict prefix of prev (rewind, retry of an earlier turn,
	// or client-side history trimming). At names the first missing item.
	Truncated
	// Incomparable: different formats, or cur is an incremental (delta) Codex
	// body, so only tools/instructions were compared.
	Incomparable
)

// Divergence is the result of comparing two prints. It is small and is stored
// (packed) in every log entry; the prints themselves are not.
type Divergence struct {
	Kind DivergenceKind
	At   SegmentRef // valid when Kind is Changed or Truncated

	ToolsBefore, ToolsAfter int  // filled when At.Kind == SegmentTools
	VolatileChanged         bool // only the excluded volatile block differed
	CacheKeyChanged         bool // Codex prompt_cache_key differs
	AddedBlocks             int  // Claude: cur.blocks - prev.blocks (lookback rule)
}

// Diverge compares p (current) against prev.
//
//	if prev == nil: return Divergence{Kind: Incomparable}
//	d.VolatileChanged = p.volatile != prev.volatile
//	d.CacheKeyChanged = p.cacheKey != prev.cacheKey
//	d.AddedBlocks     = int(p.blocks) - int(prev.blocks)
//	if p.format != prev.format: d.Kind = Incomparable; return
//	if p.incremental:
//	    compare only the tools and instructions hashes (the input is a delta);
//	    if one differs: d.Kind = Changed, d.At = that segment; else d.Kind = Incomparable
//	    return
//	// Flat comparison is valid because both lists use the same section order.
//	n := min(len(p.hashes), len(prev.hashes))
//	for i := 0; i < n; i++:
//	    if p.hashes[i] != prev.hashes[i]:
//	        d.Kind = Changed
//	        d.At = p.refAt(i)            // map flat index -> (kind, index) via p.layout
//	        if d.At.Kind == SegmentTools: d.ToolsBefore, d.ToolsAfter = prev.toolCount, p.toolCount
//	        return
//	if len(p.hashes) < len(prev.hashes): d.Kind = Truncated; d.At = prev.refAt(n); return
//	d.Kind = Extends
//
// Section-count shifts (a system block added) surface as Changed at the first
// shifted position, which is the segment a human would blame.
func (p *Print) Diverge(prev *Print) Divergence { panic("not implemented") }

// refAt maps a flat hash index to a SegmentRef using p.layout.
func (p *Print) refAt(i int) SegmentRef { panic("not implemented") }

// MarshalBinary encodes the print for the request-log snapshot (baselines of
// recently active sessions only). Layout: version byte, header fields, then
// len(hashes) little-endian uint64s.
func (p *Print) MarshalBinary() ([]byte, error) { panic("not implemented") }

// UnmarshalBinary is the inverse of MarshalBinary.
func (p *Print) UnmarshalBinary(data []byte) error { panic("not implemented") }

// --- transport from the reporter to the plugin ---

type printContextKey struct{}

// WithPrint attaches p to ctx. Called only inside UsageReporter's once.Do for the
// attempt record, so side-model records never carry a print.
func WithPrint(ctx context.Context, p *Print) context.Context { panic("not implemented") }

// FromContext returns the print attached by WithPrint, or nil.
func FromContext(ctx context.Context) *Print { panic("not implemented") }

// --- capture gate ---

// captureEnabled is flipped by the request-log service on every config refresh.
// When false, UsageReporter.CapturePrompt keeps nothing and publish hashes nothing,
// so a disabled feature costs one atomic load per request.
var captureEnabled atomic.Bool

// SetCaptureEnabled is called by requestlog.Service.refreshEnabled.
func SetCaptureEnabled(on bool) { captureEnabled.Store(on) }

// CaptureEnabled is read by UsageReporter.CapturePrompt.
func CaptureEnabled() bool { return captureEnabled.Load() }
