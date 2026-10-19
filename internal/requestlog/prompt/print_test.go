package prompt

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

const billingBlock = `{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.3; cch=%s","cache_control":{"type":"ephemeral"}}`

// claudeBody builds a Claude Messages body in the shape the proxy sends upstream:
// a billing header block, a cached system prompt, tools, and messages with the
// moving breakpoint on the last message.
func claudeBody(cch string, tools []string, thinking string, messages []string, ttl string) []byte {
	var b strings.Builder
	b.WriteString(`{"model":"claude-opus-4-5","max_tokens":1024,`)
	b.WriteString(`"tools":[` + strings.Join(tools, ",") + `],`)
	b.WriteString(`"system":[` + fmt.Sprintf(billingBlock, cch) + `,{"type":"text","text":"You are Claude Code.","cache_control":{"type":"ephemeral"` + ttl + `}}],`)
	if thinking != "" {
		b.WriteString(`"thinking":` + thinking + `,`)
	}
	b.WriteString(`"messages":[`)
	for i, m := range messages {
		if i > 0 {
			b.WriteString(",")
		}
		if i == len(messages)-1 {
			b.WriteString(`{"role":"user","content":[{"type":"text","text":"` + m + `","cache_control":{"type":"ephemeral"}}]}`)
		} else {
			b.WriteString(`{"role":"user","content":[{"type":"text","text":"` + m + `"}]}`)
		}
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

var (
	toolA = `{"name":"read","description":"read a file","input_schema":{"type":"object"}}`
	toolB = `{"name":"write","description":"write a file","input_schema":{"type":"object"}}`
)

func TestParseFormat(t *testing.T) {
	cases := map[string]Format{"claude": FormatClaudeMessages, "codex": FormatCodexResponses, "openai-response": FormatCodexResponses, "gemini": FormatUnknown, "": FormatUnknown}
	for in, want := range cases {
		if got := ParseFormat(in); got != want {
			t.Errorf("ParseFormat(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestOfRejectsUnknownAndInvalid(t *testing.T) {
	if Of([]byte(`{"messages":[]}`), FormatUnknown) != nil {
		t.Fatal("unknown format must yield nil")
	}
	if Of([]byte(`not json`), FormatClaudeMessages) != nil {
		t.Fatal("invalid body must yield nil")
	}
	if Of([]byte(`[1,2]`), FormatCodexResponses) != nil {
		t.Fatal("non-object body must yield nil")
	}
}

func TestClaudeMovingBreakpointIsNotAChange(t *testing.T) {
	prev := Of(claudeBody("aaa", []string{toolA}, "", []string{"hi", "there"}, ""), FormatClaudeMessages)
	cur := Of(claudeBody("aaa", []string{toolA}, "", []string{"hi", "there", "more"}, ""), FormatClaudeMessages)
	if prev == nil || cur == nil {
		t.Fatal("prints must not be nil")
	}
	// tools(1) + system(2) + settings(1) + messages
	if prev.Size() != 6 || cur.Size() != 7 {
		t.Fatalf("sizes = %d, %d", prev.Size(), cur.Size())
	}
	d := cur.Diverge(prev)
	if d.Kind != Extends {
		t.Fatalf("appending a message with the breakpoint moved must extend, got %+v", d)
	}
	if d.AddedBlocks != 1 {
		t.Fatalf("AddedBlocks = %d", d.AddedBlocks)
	}
	if cur.Breakpoints() != 3 {
		t.Fatalf("breakpoints = %d", cur.Breakpoints())
	}
	if ttl, class := cur.TTL(); ttl != 5*time.Minute || class != TTL5m {
		t.Fatalf("ttl = %v %v", ttl, class)
	}
}

func TestClaudeBillingHeaderIsVolatileNotASegment(t *testing.T) {
	prev := Of(claudeBody("aaa", []string{toolA}, "", []string{"hi"}, ""), FormatClaudeMessages)
	cur := Of(claudeBody("bbb", []string{toolA}, "", []string{"hi"}, ""), FormatClaudeMessages)
	d := cur.Diverge(prev)
	if d.Kind != Extends || !d.VolatileChanged {
		t.Fatalf("billing header change must be volatile only, got %+v", d)
	}
	same := Of(claudeBody("bbb", []string{toolA}, "", []string{"hi"}, ""), FormatClaudeMessages)
	if d2 := same.Diverge(cur); d2.VolatileChanged {
		t.Fatalf("identical billing header reported as volatile change: %+v", d2)
	}
}

func TestClaudeSystemBlockIndexFollowsTheBody(t *testing.T) {
	prev := Of(claudeBody("aaa", []string{toolA}, "", []string{"hi"}, ""), FormatClaudeMessages)
	body := strings.Replace(string(claudeBody("aaa", []string{toolA}, "", []string{"hi"}, "")), "You are Claude Code.", "You are someone else.", 1)
	cur := Of([]byte(body), FormatClaudeMessages)
	d := cur.Diverge(prev)
	if d.Kind != Changed || d.At != (SegmentRef{Kind: SegmentSystem, Index: 1}) {
		t.Fatalf("system prompt change must point at system block 1, got %+v", d)
	}
}

func TestClaudeSettingsSegment(t *testing.T) {
	prev := Of(claudeBody("aaa", []string{toolA}, `{"type":"enabled","budget_tokens":1024}`, []string{"hi"}, ""), FormatClaudeMessages)
	cur := Of(claudeBody("aaa", []string{toolA}, `{"type":"enabled","budget_tokens":4096}`, []string{"hi"}, ""), FormatClaudeMessages)
	d := cur.Diverge(prev)
	if d.Kind != Changed || d.At != (SegmentRef{Kind: SegmentSettings}) {
		t.Fatalf("thinking change must point at settings, got %+v", d)
	}
}

func TestClaudeToolsChanged(t *testing.T) {
	prev := Of(claudeBody("aaa", []string{toolA}, "", []string{"hi"}, ""), FormatClaudeMessages)
	cur := Of(claudeBody("aaa", []string{toolA, toolB}, "", []string{"hi"}, ""), FormatClaudeMessages)
	d := cur.Diverge(prev)
	if d.Kind != Changed || d.At != (SegmentRef{Kind: SegmentTools}) || d.ToolsBefore != 1 || d.ToolsAfter != 2 {
		t.Fatalf("tool added must point at tools 1 -> 2, got %+v", d)
	}
}

func TestClaudeToolCacheControlIgnored(t *testing.T) {
	marked := strings.TrimSuffix(toolA, "}") + `,"cache_control":{"type":"ephemeral","ttl":"1h"}}`
	prev := Of(claudeBody("aaa", []string{toolA}, "", []string{"hi"}, ""), FormatClaudeMessages)
	cur := Of(claudeBody("aaa", []string{marked}, "", []string{"hi"}, ""), FormatClaudeMessages)
	if d := cur.Diverge(prev); d.Kind != Extends {
		t.Fatalf("cache_control on a tool must not change its hash, got %+v", d)
	}
	if ttl, class := cur.TTL(); ttl != time.Hour || class != TTL1h {
		t.Fatalf("1h breakpoint must raise the TTL class, got %v %v", ttl, class)
	}
}

func TestClaudeMessageChangedAndTruncated(t *testing.T) {
	prev := Of(claudeBody("aaa", []string{toolA}, "", []string{"one", "two", "three"}, ""), FormatClaudeMessages)
	changed := Of(claudeBody("aaa", []string{toolA}, "", []string{"one", "TWO", "three"}, ""), FormatClaudeMessages)
	if d := changed.Diverge(prev); d.Kind != Changed || d.At != (SegmentRef{Kind: SegmentMessage, Index: 1}) {
		t.Fatalf("edited message must point at message 1, got %+v", d)
	}
	shorter := Of(claudeBody("aaa", []string{toolA}, "", []string{"one", "two"}, ""), FormatClaudeMessages)
	if d := shorter.Diverge(prev); d.Kind != Truncated || d.At != (SegmentRef{Kind: SegmentMessage, Index: 2}) {
		t.Fatalf("shorter history must be truncated at message 2, got %+v", d)
	}
	// The breakpoint on "two" moved when it became the last message; that must not count.
}

func TestClaudeStringSystemAndStringContent(t *testing.T) {
	body := []byte(`{"system":"be brief","messages":[{"role":"user","content":"hi"}]}`)
	p := Of(body, FormatClaudeMessages)
	if p == nil || p.Size() != 3 {
		t.Fatalf("string system + settings + one message, got %+v", p)
	}
	if p.Breakpoints() != 0 {
		t.Fatalf("no cache_control means no breakpoints, got %d", p.Breakpoints())
	}
	if ttl, class := p.TTL(); ttl != 0 || class != TTLNone {
		t.Fatalf("ttl = %v %v", ttl, class)
	}
	other := Of([]byte(`{"system":"be verbose","messages":[{"role":"user","content":"hi"}]}`), FormatClaudeMessages)
	if d := other.Diverge(p); d.Kind != Changed || d.At != (SegmentRef{Kind: SegmentSystem, Index: 0}) {
		t.Fatalf("string system change must point at system 0, got %+v", d)
	}
}

func TestClaudeSegmentCap(t *testing.T) {
	msgs := make([]string, MaxSegments+10)
	for i := range msgs {
		msgs[i] = fmt.Sprintf("m%d", i)
	}
	p := Of(claudeBody("aaa", []string{toolA}, "", msgs, ""), FormatClaudeMessages)
	if p == nil || p.Size() != MaxSegments {
		t.Fatalf("size = %d, want cap %d", p.Size(), MaxSegments)
	}
	if !p.truncatedTail {
		t.Fatal("truncatedTail must be set")
	}
	// A change inside the folded tail still surfaces as a change at the last slot.
	msgs[len(msgs)-1] = "changed"
	q := Of(claudeBody("aaa", []string{toolA}, "", msgs, ""), FormatClaudeMessages)
	d := q.Diverge(p)
	if d.Kind != Changed || d.At.Kind != SegmentMessage || d.At.Index != MaxSegments-1-4 {
		t.Fatalf("folded tail change = %+v", d)
	}
}

func codexBody(instructions, cacheKey, prevID string, tools []string, input []string) []byte {
	var b strings.Builder
	b.WriteString(`{"model":"gpt-5","store":false,`)
	if instructions != "" {
		b.WriteString(`"instructions":` + fmt.Sprintf("%q", instructions) + `,`)
	}
	if cacheKey != "" {
		b.WriteString(`"prompt_cache_key":"` + cacheKey + `",`)
	}
	if prevID != "" {
		b.WriteString(`"previous_response_id":"` + prevID + `",`)
	}
	b.WriteString(`"tools":[` + strings.Join(tools, ",") + `],"input":[` + strings.Join(input, ",") + `]}`)
	return []byte(b.String())
}

var (
	inA = `{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}`
	inB = `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}`
	inC = `{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}`
)

func TestCodexSegments(t *testing.T) {
	prev := Of(codexBody("be brief", "k1", "", []string{toolA}, []string{inA, inB}), FormatCodexResponses)
	if prev == nil || prev.Size() != 4 || prev.Format() != FormatCodexResponses {
		t.Fatalf("tools + instructions + 2 inputs, got %+v", prev)
	}
	if ttl, class := prev.TTL(); ttl != 5*time.Minute || class != TTLUnknown {
		t.Fatalf("codex ttl = %v %v", ttl, class)
	}
	ext := Of(codexBody("be brief", "k1", "", []string{toolA}, []string{inA, inB, inC}), FormatCodexResponses)
	if d := ext.Diverge(prev); d.Kind != Extends || d.CacheKeyChanged {
		t.Fatalf("appended input must extend, got %+v", d)
	}
	instr := Of(codexBody("be verbose", "k1", "", []string{toolA}, []string{inA, inB}), FormatCodexResponses)
	if d := instr.Diverge(prev); d.Kind != Changed || d.At != (SegmentRef{Kind: SegmentInstructions}) {
		t.Fatalf("instructions change = %+v", d)
	}
	item := Of(codexBody("be brief", "k1", "", []string{toolA}, []string{inA, inC}), FormatCodexResponses)
	if d := item.Diverge(prev); d.Kind != Changed || d.At != (SegmentRef{Kind: SegmentInput, Index: 1}) {
		t.Fatalf("input change = %+v", d)
	}
	key := Of(codexBody("be brief", "k2", "", []string{toolA}, []string{inA, inB}), FormatCodexResponses)
	if d := key.Diverge(prev); d.Kind != Extends || !d.CacheKeyChanged {
		t.Fatalf("cache key change = %+v", d)
	}
	tools := Of(codexBody("be brief", "k1", "", []string{toolA, toolB}, []string{inA, inB}), FormatCodexResponses)
	if d := tools.Diverge(prev); d.Kind != Changed || d.At.Kind != SegmentTools || d.ToolsBefore != 1 || d.ToolsAfter != 2 {
		t.Fatalf("tools change = %+v", d)
	}
}

func TestCodexIncrementalComparesOnlyToolsAndInstructions(t *testing.T) {
	prev := Of(codexBody("be brief", "k1", "", []string{toolA}, []string{inA, inB}), FormatCodexResponses)
	delta := Of(codexBody("be brief", "k1", "resp_1", []string{toolA}, []string{inC}), FormatCodexResponses)
	if d := delta.Diverge(prev); d.Kind != Incomparable {
		t.Fatalf("delta input must be incomparable, got %+v", d)
	}
	deltaTools := Of(codexBody("be brief", "k1", "resp_1", []string{toolB}, []string{inC}), FormatCodexResponses)
	if d := deltaTools.Diverge(prev); d.Kind != Changed || d.At.Kind != SegmentTools {
		t.Fatalf("delta with changed tools = %+v", d)
	}
}

func TestDivergeAcrossFormatsAndNil(t *testing.T) {
	claude := Of(claudeBody("aaa", []string{toolA}, "", []string{"hi"}, ""), FormatClaudeMessages)
	codex := Of(codexBody("x", "", "", nil, []string{inA}), FormatCodexResponses)
	if d := claude.Diverge(codex); d.Kind != Incomparable {
		t.Fatalf("cross-format = %+v", d)
	}
	if d := claude.Diverge(nil); d.Kind != Incomparable {
		t.Fatalf("nil prev = %+v", d)
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	for _, p := range []*Print{
		Of(claudeBody("aaa", []string{toolA, toolB}, `{"type":"enabled","budget_tokens":1}`, []string{"one", "two"}, `,"ttl":"1h"`), FormatClaudeMessages),
		Of(codexBody("be brief", "k1", "", []string{toolA}, []string{inA}), FormatCodexResponses),
	} {
		data, err := p.MarshalBinary()
		if err != nil {
			t.Fatal(err)
		}
		var q Print
		if err = q.UnmarshalBinary(data); err != nil {
			t.Fatal(err)
		}
		if d := q.Diverge(p); d.Kind != Extends || d.VolatileChanged || d.CacheKeyChanged || d.AddedBlocks != 0 {
			t.Fatalf("round trip diverged: %+v", d)
		}
		if q.Size() != p.Size() || q.Breakpoints() != p.Breakpoints() || q.toolCount != p.toolCount || q.incremental != p.incremental {
			t.Fatalf("round trip lost header fields: %+v vs %+v", q, *p)
		}
		if ttlQ, classQ := q.TTL(); ttlQ != mustTTL(p) || classQ != p.ttl {
			t.Fatalf("round trip ttl mismatch")
		}
	}
	delta := Of(codexBody("be brief", "k1", "resp_1", []string{toolA}, []string{inA}), FormatCodexResponses)
	data, err := delta.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var back Print
	if err = back.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	if !back.incremental || back.Diverge(delta).Kind != Incomparable {
		t.Fatalf("incremental flag lost in round trip: %+v", back)
	}
	var bad Print
	if err = bad.UnmarshalBinary([]byte{9, 9, 9}); err == nil {
		t.Fatal("short data must fail")
	}
}

func mustTTL(p *Print) time.Duration { d, _ := p.TTL(); return d }

func TestContextTransport(t *testing.T) {
	p := Of(codexBody("x", "", "", nil, []string{inA}), FormatCodexResponses)
	ctx := WithPrint(context.Background(), p)
	if FromContext(ctx) != p {
		t.Fatal("print not returned from context")
	}
	if FromContext(context.Background()) != nil {
		t.Fatal("empty context must yield nil")
	}
	if FromContext(WithPrint(context.Background(), nil)) != nil {
		t.Fatal("nil print must stay nil")
	}
}

func TestCaptureGate(t *testing.T) {
	SetCaptureEnabled(false)
	if CaptureEnabled() {
		t.Fatal("gate must start closed")
	}
	SetCaptureEnabled(true)
	defer SetCaptureEnabled(false)
	if !CaptureEnabled() {
		t.Fatal("gate must open")
	}
}

// multiBlockBody builds a Claude body whose messages each hold several text blocks.
func multiBlockBody(messages ...[]string) []byte {
	var b strings.Builder
	b.WriteString(`{"system":"be brief","messages":[`)
	for i, blocks := range messages {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"role":"user","content":[`)
		for j, text := range blocks {
			if j > 0 {
				b.WriteString(",")
			}
			b.WriteString(`{"type":"text","text":"` + text + `"}`)
		}
		b.WriteString(`]}`)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

func TestClaudeBlocksAreSegmentsReportedByMessage(t *testing.T) {
	prev := Of(multiBlockBody([]string{"ctx"}, []string{"<t>", "a", "b", "c", "act1"}), FormatClaudeMessages)
	if prev.Size() != 2+6 {
		t.Fatalf("system + settings + 6 blocks, got %d", prev.Size())
	}
	tail := Of(multiBlockBody([]string{"ctx"}, []string{"<t>", "a", "b", "c", "d", "act2"}), FormatClaudeMessages)
	if d := tail.Diverge(prev); d.Kind != Changed || d.At != (SegmentRef{Kind: SegmentMessage, Index: 1}) {
		t.Fatalf("a block change inside message 1 must name message 1, got %+v", d)
	}
	if got := tail.SharedPrefix(prev); got != 2+5 {
		t.Fatalf("a rewritten tail shares every block before it: %d", got)
	}
	sibling := Of(multiBlockBody([]string{"ctx"}, []string{"<t>", "x", "y"}), FormatClaudeMessages)
	if got := sibling.SharedPrefix(prev); got != 2+2 {
		t.Fatalf("another transcript shares only the preamble: %d", got)
	}
	moved := Of(multiBlockBody([]string{"ctx", "<t>"}, []string{"a", "b", "c", "act1"}), FormatClaudeMessages)
	if d := moved.Diverge(prev); d.Kind != Changed || d.At != (SegmentRef{Kind: SegmentMessage, Index: 0}) {
		t.Fatalf("a block that moves to another message is a change, got %+v", d)
	}
	empty := Of([]byte(`{"messages":[{"role":"user","content":[]},{"role":"user","content":"hi"}]}`), FormatClaudeMessages)
	if empty.Size() != 1+2 || empty.refAt(2) != (SegmentRef{Kind: SegmentMessage, Index: 1}) {
		t.Fatalf("a message with no blocks still takes one slot: size %d", empty.Size())
	}
}

func TestSharedPrefixAcrossFormatsAndDeltas(t *testing.T) {
	claude := Of(claudeBody("aaa", []string{toolA}, "", []string{"one"}, ""), FormatClaudeMessages)
	codex := Of(codexBody("be brief", "k1", "", []string{toolA}, []string{inA}), FormatCodexResponses)
	delta := Of(codexBody("be brief", "k1", "resp_1", []string{toolA}, []string{inA}), FormatCodexResponses)
	if claude.SharedPrefix(codex) != 0 || delta.SharedPrefix(codex) != 0 || codex.SharedPrefix(nil) != 0 {
		t.Fatal("prints of another format, deltas and nil share nothing")
	}
	if codex.SharedPrefix(codex) != codex.Size() {
		t.Fatal("a print shares its whole prefix with itself")
	}
}

func TestMarshalKeepsMessageStartsAndRejectsVersion1(t *testing.T) {
	p := Of(multiBlockBody([]string{"ctx"}, []string{"<t>", "a", "b"}, []string{"c"}), FormatClaudeMessages)
	data, err := p.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	var q Print
	if err = q.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < p.Size(); i++ {
		if q.refAt(i) != p.refAt(i) {
			t.Fatalf("round trip moved segment %d: %+v vs %+v", i, q.refAt(i), p.refAt(i))
		}
	}
	old := append([]byte(nil), data...)
	old[0] = 1
	if err = q.UnmarshalBinary(old); err == nil {
		t.Fatal("a version 1 print hashed whole messages and must be rejected")
	}
	bad := append([]byte(nil), data...)
	bad[len(bad)-4] = 0 // last message start moved back to 0: out of order
	if err = q.UnmarshalBinary(bad); err == nil {
		t.Fatal("message starts out of order must be rejected")
	}
}
