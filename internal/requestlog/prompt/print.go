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
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tidwall/gjson"
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

// ParseFormat maps the executor's translator format string ("claude", "codex",
// "openai-response") to a Format. Unknown strings return FormatUnknown.
func ParseFormat(s string) Format {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "claude":
		return FormatClaudeMessages
	case "codex", "openai-response":
		return FormatCodexResponses
	}
	return FormatUnknown
}

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
	// where items are Claude content blocks or Codex input items. Each hash is the first
	// 8 bytes of sha256 over the segment's canonical bytes (see canonical rules in
	// claude.go / codex.go). Capped at MaxSegments; beyond the cap the tail is
	// folded into one final hash and truncatedTail is set.
	hashes []uint64
	layout layout
	// msgStarts (Claude only) is the item index where each message begins,
	// ascending, starting at 0. It maps a block back to its message for reports.
	msgStarts []uint32

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
	system   uint16 // Claude system blocks (a volatile block keeps its slot with a constant hash); Codex: 0
	settings uint8  // 0 or 1 (Claude only)
	instr    uint8  // 0 or 1 (Codex only)
	items    uint32 // content blocks (Claude) or input items (Codex)
}

// MaxSegments bounds one Print to 4096 hashes (32 KiB). The largest Claude Code
// request seen held about 1,500 content blocks; the cap only guards pathological
// clients.
const MaxSegments = 4096

// volatileSlot is the constant hash stored in the prefix for a volatile block.
// Keeping the slot keeps body indexes intact; the real hash lives in Print.volatile.
const volatileSlot uint64 = 0

// Of fingerprints body. It returns nil for FormatUnknown or an invalid body.
// It never returns an error: a fingerprint is diagnostics, never a reason to
// fail a request.
//
// Cost: one pass over body with gjson plus sha256 over each segment's bytes,
// about 1 ms per MB. Called by UsageReporter at publish time, after the upstream
// response is fully consumed, never before the upstream request is sent.
func Of(body []byte, format Format) *Print {
	switch format {
	case FormatClaudeMessages:
		return claudePrint(body)
	case FormatCodexResponses:
		return codexPrint(body)
	default:
		return nil
	}
}

// Size returns the number of hashes held, used by the session tracker's hash budget.
func (p *Print) Size() int {
	if p == nil {
		return 0
	}
	return len(p.hashes)
}

// Format returns the wire format the print was computed from.
func (p *Print) Format() Format {
	if p == nil {
		return FormatUnknown
	}
	return p.format
}

// TTL returns the longest requested cache TTL as a duration:
// TTL5m -> 5m, TTL1h -> 1h, TTLUnknown -> 5m (heuristic, low confidence), TTLNone -> 0.
func (p *Print) TTL() (time.Duration, TTLClass) {
	if p == nil {
		return 0, TTLNone
	}
	switch p.ttl {
	case TTL5m, TTLUnknown:
		return 5 * time.Minute, p.ttl
	case TTL1h:
		return time.Hour, p.ttl
	default:
		return 0, TTLNone
	}
}

// Breakpoints returns the number of cache_control markers (Claude only).
func (p *Print) Breakpoints() int {
	if p == nil {
		return 0
	}
	return int(p.breakpoints)
}

// SharedPrefix returns how many leading hashes p and other have in common.
// It is 0 across formats and for incremental (delta) Codex bodies, whose input
// is not a full prefix. The session tracker uses it to pick, among one model's
// lanes, the conversation a request continues.
func (p *Print) SharedPrefix(other *Print) int {
	if p == nil || other == nil || p.format != other.format || p.incremental || other.incremental {
		return 0
	}
	n := min(len(p.hashes), len(other.hashes))
	for i := 0; i < n; i++ {
		if p.hashes[i] != other.hashes[i] {
			return i
		}
	}
	return n
}

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

// Diverge compares p (current) against prev. Section-count shifts (a system
// block added) surface as Changed at the first shifted position, which is the
// segment a human would blame.
func (p *Print) Diverge(prev *Print) Divergence {
	if p == nil || prev == nil {
		return Divergence{Kind: Incomparable}
	}
	d := Divergence{
		VolatileChanged: p.volatile != prev.volatile,
		CacheKeyChanged: p.cacheKey != prev.cacheKey,
		AddedBlocks:     int(p.blocks) - int(prev.blocks),
	}
	if p.format != prev.format {
		d.Kind = Incomparable
		return d
	}
	if p.incremental {
		for _, kind := range []SegmentKind{SegmentTools, SegmentInstructions} {
			curHash, curOK := p.sectionHash(kind)
			prevHash, prevOK := prev.sectionHash(kind)
			if curHash == prevHash && curOK == prevOK {
				continue
			}
			d.Kind = Changed
			d.At = SegmentRef{Kind: kind}
			if kind == SegmentTools {
				d.ToolsBefore, d.ToolsAfter = int(prev.toolCount), int(p.toolCount)
			}
			return d
		}
		d.Kind = Incomparable
		return d
	}
	n := min(len(p.hashes), len(prev.hashes))
	for i := 0; i < n; i++ {
		if p.hashes[i] != prev.hashes[i] {
			d.Kind = Changed
			d.At = p.refAt(i)
			if d.At.Kind == SegmentTools {
				d.ToolsBefore, d.ToolsAfter = int(prev.toolCount), int(p.toolCount)
			}
			return d
		}
	}
	if len(p.hashes) < len(prev.hashes) {
		d.Kind = Truncated
		d.At = prev.refAt(n)
		return d
	}
	d.Kind = Extends
	return d
}

// sectionHash returns the hash of a single-slot section and whether it exists.
func (p *Print) sectionHash(kind SegmentKind) (uint64, bool) {
	l := p.layout
	switch kind {
	case SegmentTools:
		if l.tools == 0 {
			return 0, false
		}
		return p.hashes[0], true
	case SegmentInstructions:
		if l.instr == 0 {
			return 0, false
		}
		return p.hashes[int(l.tools)+int(l.system)+int(l.settings)], true
	}
	return 0, false
}

// refAt maps a flat hash index to a SegmentRef using p.layout.
func (p *Print) refAt(i int) SegmentRef {
	l := p.layout
	if i < int(l.tools) {
		return SegmentRef{Kind: SegmentTools}
	}
	i -= int(l.tools)
	if i < int(l.system) {
		return SegmentRef{Kind: SegmentSystem, Index: i}
	}
	i -= int(l.system)
	if i < int(l.settings) {
		return SegmentRef{Kind: SegmentSettings}
	}
	i -= int(l.settings)
	if i < int(l.instr) {
		return SegmentRef{Kind: SegmentInstructions}
	}
	i -= int(l.instr)
	if p.format == FormatCodexResponses {
		return SegmentRef{Kind: SegmentInput, Index: i}
	}
	// The message holding block i is the last one that starts at or before it.
	msg := sort.Search(len(p.msgStarts), func(m int) bool { return p.msgStarts[m] > uint32(i) }) - 1
	return SegmentRef{Kind: SegmentMessage, Index: max(msg, 0)}
}

// fold enforces MaxSegments: the tail beyond the cap is hashed into one final
// slot and the layout counts are shrunk, in section order, to match.
func (p *Print) fold() {
	if len(p.hashes) <= MaxSegments {
		return
	}
	h := sha256.New()
	var buf [8]byte
	for _, x := range p.hashes[MaxSegments-1:] {
		binary.LittleEndian.PutUint64(buf[:], x)
		_, _ = h.Write(buf[:])
	}
	p.hashes = append(p.hashes[:MaxSegments-1], sum64(h))
	p.truncatedTail = true
	remaining := len(p.hashes)
	p.layout.tools = uint8(min(int(p.layout.tools), remaining))
	remaining -= int(p.layout.tools)
	p.layout.system = uint16(min(int(p.layout.system), remaining))
	remaining -= int(p.layout.system)
	p.layout.settings = uint8(min(int(p.layout.settings), remaining))
	remaining -= int(p.layout.settings)
	p.layout.instr = uint8(min(int(p.layout.instr), remaining))
	remaining -= int(p.layout.instr)
	p.layout.items = uint32(remaining)
	for len(p.msgStarts) > 0 && p.msgStarts[len(p.msgStarts)-1] >= p.layout.items {
		p.msgStarts = p.msgStarts[:len(p.msgStarts)-1]
	}
}

// binaryVersion 2 hashes Claude content blocks and appends msgStarts. A version 1
// print hashed whole messages, so it cannot be compared with a version 2 print:
// UnmarshalBinary rejects it and the tracker skips that lane on restore.
const binaryVersion = 2

// binaryHeaderLen is the fixed-size part of MarshalBinary's output.
const binaryHeaderLen = 40

// MarshalBinary encodes the print for the request-log snapshot (baselines of
// recently active sessions only). Layout: version byte, header fields,
// len(hashes) little-endian uint64s, then a uint32 count and the msgStarts.
func (p *Print) MarshalBinary() ([]byte, error) {
	if p == nil {
		return nil, errors.New("prompt: marshal nil print")
	}
	out := make([]byte, 0, binaryHeaderLen+8*len(p.hashes)+4+4*len(p.msgStarts))
	out = append(out, binaryVersion, byte(p.format))
	out = binary.LittleEndian.AppendUint16(out, p.toolCount)
	out = binary.LittleEndian.AppendUint32(out, p.blocks)
	out = append(out, p.breakpoints, byte(p.ttl))
	out = binary.LittleEndian.AppendUint64(out, p.volatile)
	out = binary.LittleEndian.AppendUint64(out, p.cacheKey)
	var flags byte
	if p.incremental {
		flags |= 1
	}
	if p.truncatedTail {
		flags |= 2
	}
	out = append(out, flags, p.layout.tools)
	out = binary.LittleEndian.AppendUint16(out, p.layout.system)
	out = append(out, p.layout.settings, p.layout.instr)
	out = binary.LittleEndian.AppendUint32(out, p.layout.items)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(p.hashes)))
	for _, x := range p.hashes {
		out = binary.LittleEndian.AppendUint64(out, x)
	}
	out = binary.LittleEndian.AppendUint32(out, uint32(len(p.msgStarts)))
	for _, x := range p.msgStarts {
		out = binary.LittleEndian.AppendUint32(out, x)
	}
	return out, nil
}

// UnmarshalBinary is the inverse of MarshalBinary.
func (p *Print) UnmarshalBinary(data []byte) error {
	if p == nil {
		return errors.New("prompt: unmarshal into nil print")
	}
	if len(data) < binaryHeaderLen || data[0] != binaryVersion {
		return errors.New("prompt: unsupported print encoding")
	}
	q := Print{format: Format(data[1])}
	q.toolCount = binary.LittleEndian.Uint16(data[2:])
	q.blocks = binary.LittleEndian.Uint32(data[4:])
	q.breakpoints = data[8]
	q.ttl = TTLClass(data[9])
	q.volatile = binary.LittleEndian.Uint64(data[10:])
	q.cacheKey = binary.LittleEndian.Uint64(data[18:])
	q.incremental = data[26]&1 != 0
	q.truncatedTail = data[26]&2 != 0
	q.layout.tools = data[27]
	q.layout.system = binary.LittleEndian.Uint16(data[28:])
	q.layout.settings = data[30]
	q.layout.instr = data[31]
	q.layout.items = binary.LittleEndian.Uint32(data[32:])
	n := int(binary.LittleEndian.Uint32(data[36:]))
	starts := binaryHeaderLen + 8*n
	if n > MaxSegments || len(data) < starts+4 {
		return errors.New("prompt: print encoding length mismatch")
	}
	m := int(binary.LittleEndian.Uint32(data[starts:]))
	if m > n || len(data) != starts+4+4*m {
		return errors.New("prompt: print encoding length mismatch")
	}
	if int(q.layout.tools)+int(q.layout.system)+int(q.layout.settings)+int(q.layout.instr)+int(q.layout.items) != n {
		return errors.New("prompt: print layout does not match hash count")
	}
	q.hashes = make([]uint64, n)
	for i := range q.hashes {
		q.hashes[i] = binary.LittleEndian.Uint64(data[binaryHeaderLen+8*i:])
	}
	if m > 0 {
		q.msgStarts = make([]uint32, m)
		for i := range q.msgStarts {
			q.msgStarts[i] = binary.LittleEndian.Uint32(data[starts+4+4*i:])
			if q.msgStarts[i] >= q.layout.items || (i == 0 && q.msgStarts[i] != 0) || (i > 0 && q.msgStarts[i] <= q.msgStarts[i-1]) {
				return errors.New("prompt: print message starts out of order")
			}
		}
	}
	*p = q
	return nil
}

// --- hashing ---

// hasher wraps one sha256 state reused across the segments of a body.
type hasher struct{ h hash.Hash }

func newHasher() *hasher { return &hasher{h: sha256.New()} }

func (h *hasher) reset()         { h.h.Reset() }
func (h *hasher) write(s string) { _, _ = h.h.Write([]byte(s)) }
func (h *hasher) sep()           { _, _ = h.h.Write([]byte{0}) }
func (h *hasher) sum() uint64    { return sum64(h.h) }

func (h *hasher) hashString(s string) uint64 {
	h.reset()
	h.write(s)
	return h.sum()
}

// writeMembers hashes every member of obj except cache_control as key, value
// raw bytes in body order. It reports whether a cache_control marker was found
// and whether that marker asked for the 1h TTL.
func (h *hasher) writeMembers(obj gjson.Result) (breakpoint bool, ttl1h bool) {
	obj.ForEach(func(k, v gjson.Result) bool {
		if k.Str == "cache_control" {
			breakpoint = true
			if v.Get("ttl").Str == "1h" {
				ttl1h = true
			}
			return true
		}
		h.write(k.Raw)
		h.sep()
		h.write(v.Raw)
		h.sep()
		return true
	})
	return breakpoint, ttl1h
}

func sum64(h hash.Hash) uint64 {
	var sum [sha256.Size]byte
	return binary.BigEndian.Uint64(h.Sum(sum[:0]))
}

// --- transport from the reporter to the plugin ---

type printContextKey struct{}

// WithPrint attaches p to ctx. Called only inside UsageReporter's once.Do for the
// attempt record, so side-model records never carry a print.
func WithPrint(ctx context.Context, p *Print) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if p == nil {
		return ctx
	}
	return context.WithValue(ctx, printContextKey{}, p)
}

// FromContext returns the print attached by WithPrint, or nil.
func FromContext(ctx context.Context) *Print {
	if ctx == nil {
		return nil
	}
	p, _ := ctx.Value(printContextKey{}).(*Print)
	return p
}

// --- capture gate ---

// captureEnabled is flipped by the request-log service on every config refresh.
// When false, UsageReporter.CapturePrompt keeps nothing and publish hashes nothing,
// so a disabled feature costs one atomic load per request.
var captureEnabled atomic.Bool

// SetCaptureEnabled is called by requestlog.Service.refreshEnabled.
func SetCaptureEnabled(on bool) { captureEnabled.Store(on) }

// CaptureEnabled is read by UsageReporter.CapturePrompt.
func CaptureEnabled() bool { return captureEnabled.Load() }
