package requestlog

import (
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/requestlog/prompt"
	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

func TestTokensFromDetailNeverUsesCachedTokens(t *testing.T) {
	// parseClaudeUsageNode copies the creation count into CachedTokens when the
	// read count is zero; a cache write must not look like a read.
	claudeWrite := coreusage.Detail{InputTokens: 12, OutputTokens: 30, ReasoningTokens: 5, CachedTokens: 1460, CacheReadTokens: 0, CacheCreationTokens: 1460}
	claudeWrite.TokenBreakdown = coreusage.NewIndependentTokenBreakdown(12, 0, 1460, 25, 5, 0)
	got, known := tokensFromDetail(claudeWrite)
	if !known || got != (tokenCounts{uncached: 12, read: 0, write: 1460, output: 30, reasoning: 5}) {
		t.Fatalf("claude write = %+v known=%v", got, known)
	}
	if got.promptTotal() != 1472 {
		t.Fatalf("prompt total = %d", got.promptTotal())
	}

	// Codex subset semantics: input_tokens includes cached_tokens.
	codex := coreusage.Detail{InputTokens: 10240, OutputTokens: 100, CachedTokens: 9984, CacheReadTokens: 9984}
	codex.TokenBreakdown = coreusage.NewSubsetTokenBreakdown(10240, 9984, 0, 100, 0, 10340)
	got, known = tokensFromDetail(codex)
	if !known || got != (tokenCounts{uncached: 256, read: 9984, write: 0, output: 100}) {
		t.Fatalf("codex = %+v known=%v", got, known)
	}

	// Fallback when the breakdown is invalid: explicit fields, still never CachedTokens.
	raw := coreusage.Detail{InputTokens: 7, OutputTokens: 3, CachedTokens: 900, CacheCreationTokens: 900}
	got, known = tokensFromDetail(raw)
	if !known || got != (tokenCounts{uncached: 7, read: 0, write: 900, output: 3}) {
		t.Fatalf("fallback = %+v known=%v", got, known)
	}
	if _, known = tokensFromDetail(coreusage.Detail{}); known {
		t.Fatal("empty detail must be unknown")
	}
}

func TestCauseSetAndPackedSegment(t *testing.T) {
	var s CauseSet
	s.Add(CauseTools)
	s.Add(CauseIdle)
	if !s.Has(CauseTools) || !s.Has(CauseIdle) || s.Has(CauseModel) {
		t.Fatalf("cause set = %b", s)
	}
	ref := prompt.SegmentRef{Kind: prompt.SegmentMessage, Index: 70000}
	if got := packSegment(ref).unpack(); got != ref {
		t.Fatalf("packed segment round trip = %+v", got)
	}
}

func TestJudgeNotApplicable(t *testing.T) {
	p := claudePrintOf(claudeOpts{messages: []string{"hi"}})
	sess := &session{id: "s", lanes: []lane{laneAt("opus", "a", t0, 5000, 5*time.Minute, p)}}
	cases := []struct {
		name string
		obs  observation
		sess *session
		want NAReason
	}{
		{"failed", func() observation {
			o := obsAt(t0.Add(time.Minute), "s", "opus", "a", 1, 5000, 0, p)
			o.failed = true
			return o
		}(), sess, NAFailed},
		{"no print", obsAt(t0.Add(time.Minute), "s", "opus", "a", 1, 5000, 0, nil), sess, NAUnsupported},
		{"no usage", func() observation {
			o := obsAt(t0.Add(time.Minute), "s", "opus", "a", 1, 5000, 0, p)
			o.tokensKnown = false
			return o
		}(), sess, NANoUsage},
		{"no session", obsAt(t0.Add(time.Minute), "", "opus", "a", 1, 5000, 0, p), nil, NANoSession},
		{"concurrent", obsAt(t0.Add(-time.Minute), "s", "opus", "a", 1, 5000, 0, p), sess, NAConcurrent},
		{"below minimum", obsAt(t0.Add(time.Minute), "s", "opus", "a", 1, 0, 0, p), &session{id: "s", lanes: []lane{laneAt("opus", "a", t0, 500, 5*time.Minute, p)}}, NABelowMinimum},
	}
	for _, tc := range cases {
		v, upd := judgeIn(tc.obs, tc.sess)
		if v.Kind != VerdictNA || v.NA != tc.want {
			t.Errorf("%s: verdict = %+v, want NA %v", tc.name, v, tc.want)
		}
		wantUpd := tc.want == NABelowMinimum
		if (upd != nil) != wantUpd {
			t.Errorf("%s: lane update = %v, want %v", tc.name, upd != nil, wantUpd)
		}
	}
}

func TestJudgeColdSetsBaseline(t *testing.T) {
	p := claudePrintOf(claudeOpts{messages: []string{"hi"}})
	v, upd := judgeIn(obsAt(t0, "s", "opus", "a", 100, 2000, 3000, p), nil)
	if v.Kind != VerdictCold || upd == nil {
		t.Fatalf("first request = %+v upd=%v", v, upd)
	}
	if upd.cachedPrefix != 5000 || upd.ttl != 5*time.Minute || upd.print != p || upd.model != "opus" || upd.authID != "a" || !upd.touchedAt.Equal(t0.Add(time.Second)) {
		t.Fatalf("claude lane = %+v", *upd)
	}
	// Cold with a read > 0 is possible when another session shares the prefix.
	if v, _ = judgeIn(obsAt(t0, "s", "opus", "a", 100, 2000, 0, p), &session{id: "s"}); v.Kind != VerdictCold {
		t.Fatalf("empty session = %+v", v)
	}
	c := codexPrintOf(codexOpts{input: []string{testInA}})
	_, upd = judgeIn(obsAt(t0, "s", "gpt-5", "a", 300, 9984, 0, c), nil)
	if upd == nil || upd.cachedPrefix != 10240 || upd.ttl != codexIdleGuess {
		t.Fatalf("codex lane = %+v", upd)
	}
}

// Prompt caches are per model, so a model can never read another model's
// prefix. The first request on a new model in a session is cold, tagged with
// model_changed, and never reports tokens "lost" against another model's lane.
func TestJudgeNewModelIsCold(t *testing.T) {
	p := claudePrintOf(claudeOpts{messages: []string{"one", "two"}})
	sess := &session{id: "s", lanes: []lane{laneAt("opus", "a", t0, 250000, 5*time.Minute, p)}}
	v, upd := judgeIn(obsAt(t0.Add(30*time.Second), "s", "sonnet", "a", 90, 117000, 124, p), sess)
	if v.Kind != VerdictCold || v.Primary != CauseModel || !v.Causes.Has(CauseModel) || v.Expected != 0 {
		t.Fatalf("first sonnet after opus = %+v", v)
	}
	if upd == nil || upd.model != "sonnet" {
		t.Fatalf("new model must start its own lane, got %+v", upd)
	}
	if c := cacheOf(v, 117000); c.Verdict != "cold" || c.Cause != "model_changed" || c.Lost != 0 || c.Expected != 0 {
		t.Fatalf("rendered = %+v", c)
	}
	if c := cacheOf(Verdict{Kind: VerdictCold}, 0); c.Cause != "" {
		t.Fatalf("a session's first request carries no cause, got %+v", c)
	}
}

func TestJudgeHitPartialMissThresholds(t *testing.T) {
	claude := claudePrintOf(claudeOpts{messages: []string{"hi"}})
	codex := codexPrintOf(codexOpts{input: []string{testInA}})
	cases := []struct {
		name     string
		p        *prompt.Print
		model    string
		expected int64
		read     int64
		want     VerdictKind
	}{
		{"claude exact", claude, "opus", 10000, 10000, VerdictHit},
		{"claude within tolerance", claude, "opus", 10000, 9750, VerdictHit},
		{"claude partial", claude, "opus", 10000, 9700, VerdictPartial},
		{"claude miss", claude, "opus", 10000, 0, VerdictMiss},
		{"claude large tolerance is 2%", claude, "opus", 100000, 98100, VerdictHit},
		{"codex within 5%", codex, "gpt-5", 10240, 9800, VerdictHit},
		{"codex partial", codex, "gpt-5", 10240, 9000, VerdictPartial},
		{"codex miss", codex, "gpt-5", 10240, 0, VerdictMiss},
	}
	for _, tc := range cases {
		sess := &session{id: "s", lanes: []lane{laneAt(tc.model, "a", t0, tc.expected, 5*time.Minute, tc.p)}}
		v, upd := judgeIn(obsAt(t0.Add(30*time.Second), "s", tc.model, "a", 100, tc.read, 0, tc.p), sess)
		if v.Kind != tc.want {
			t.Errorf("%s: verdict = %v (%+v), want %v", tc.name, v.Kind, v, tc.want)
		}
		if upd == nil {
			t.Errorf("%s: successful attempt must update the lane", tc.name)
		}
		if v.Expected != uint32(tc.expected) || v.GapSec != 30 || v.TTLSec != 300 {
			t.Errorf("%s: expected/gap/ttl = %d/%d/%d", tc.name, v.Expected, v.GapSec, v.TTLSec)
		}
	}
}

func TestJudgeCauses(t *testing.T) {
	base := claudePrintOf(claudeOpts{messages: []string{"one", "two"}})
	codexBase := codexPrintOf(codexOpts{cacheKey: "k1", input: []string{testInA}})
	claudeLane := func(auth string, touched time.Time, ttl time.Duration) *session {
		return &session{id: "s", lanes: []lane{laneAt("opus", auth, touched, 10000, ttl, base)}}
	}
	codexLane := func(touched time.Time) *session {
		return &session{id: "s", lanes: []lane{laneAt("gpt-5", "a", touched, 10240, codexIdleGuess, codexBase)}}
	}
	soon := t0.Add(30 * time.Second)
	late := t0.Add(20 * time.Minute)
	cases := []struct {
		name       string
		obs        observation
		sess       *session
		kind       VerdictKind
		primary    Cause
		confidence Confidence
		causes     []Cause
		segment    prompt.SegmentRef
		tools      [2]uint16
	}{
		{
			name: "credential beats everything", kind: VerdictMiss, primary: CauseCredential, confidence: ConfidenceHigh,
			obs:    obsAt(late, "s", "opus", "b", 100, 0, 10000, claudePrintOf(claudeOpts{tools: []string{testToolA, testToolB}, messages: []string{"one", "two"}})),
			sess:   claudeLane("a", t0, 5*time.Minute),
			causes: []Cause{CauseCredential, CauseTools, CauseIdle}, segment: prompt.SegmentRef{Kind: prompt.SegmentTools}, tools: [2]uint16{1, 2},
		},
		{
			name: "idle beats tools on a miss", kind: VerdictMiss, primary: CauseIdle, confidence: ConfidenceHigh,
			obs:    obsAt(late, "s", "opus", "a", 100, 0, 10000, claudePrintOf(claudeOpts{tools: []string{testToolA, testToolB}, messages: []string{"one", "two"}})),
			sess:   claudeLane("a", t0, 5*time.Minute),
			causes: []Cause{CauseTools, CauseIdle}, segment: prompt.SegmentRef{Kind: prompt.SegmentTools}, tools: [2]uint16{1, 2},
		},
		{
			name: "tools beat idle on a partial read", kind: VerdictPartial, primary: CauseTools, confidence: ConfidenceHigh,
			obs:    obsAt(late, "s", "opus", "a", 100, 4000, 6000, claudePrintOf(claudeOpts{tools: []string{testToolA, testToolB}, messages: []string{"one", "two"}})),
			sess:   claudeLane("a", t0, 5*time.Minute),
			causes: []Cause{CauseTools, CauseIdle}, segment: prompt.SegmentRef{Kind: prompt.SegmentTools}, tools: [2]uint16{1, 2},
		},
		{
			name: "idle within a 1h ttl is not idle", kind: VerdictMiss, primary: CauseUnexplained, confidence: ConfidenceLow,
			obs:    obsAt(late, "s", "opus", "a", 100, 0, 0, base),
			sess:   claudeLane("a", t0, time.Hour),
			causes: []Cause{CauseUnexplained},
		},
		{
			name: "system block changed", kind: VerdictPartial, primary: CauseSystem, confidence: ConfidenceHigh,
			obs:    obsAt(soon, "s", "opus", "a", 100, 3000, 7000, claudePrintOf(claudeOpts{system: "You are someone else.", messages: []string{"one", "two"}})),
			sess:   claudeLane("a", t0, 5*time.Minute),
			causes: []Cause{CauseSystem}, segment: prompt.SegmentRef{Kind: prompt.SegmentSystem, Index: 1},
		},
		{
			name: "settings changed", kind: VerdictPartial, primary: CauseSettings, confidence: ConfidenceHigh,
			obs:    obsAt(soon, "s", "opus", "a", 100, 3000, 7000, claudePrintOf(claudeOpts{thinking: `{"type":"enabled","budget_tokens":2048}`, messages: []string{"one", "two"}})),
			sess:   claudeLane("a", t0, 5*time.Minute),
			causes: []Cause{CauseSettings}, segment: prompt.SegmentRef{Kind: prompt.SegmentSettings},
		},
		{
			name: "message changed", kind: VerdictPartial, primary: CauseMessage, confidence: ConfidenceHigh,
			obs:    obsAt(soon, "s", "opus", "a", 100, 3000, 7000, claudePrintOf(claudeOpts{messages: []string{"one", "TWO"}})),
			sess:   claudeLane("a", t0, 5*time.Minute),
			causes: []Cause{CauseMessage}, segment: prompt.SegmentRef{Kind: prompt.SegmentMessage, Index: 1},
		},
		{
			name: "history truncated caps expected", kind: VerdictMiss, primary: CauseTruncated, confidence: ConfidenceHigh,
			obs:    obsAt(soon, "s", "opus", "a", 2000, 0, 0, claudePrintOf(claudeOpts{messages: []string{"one"}})),
			sess:   claudeLane("a", t0, 5*time.Minute),
			causes: []Cause{CauseTruncated}, segment: prompt.SegmentRef{Kind: prompt.SegmentMessage, Index: 1},
		},
		{
			name: "no breakpoint", kind: VerdictMiss, primary: CauseNoBreakpoint, confidence: ConfidenceLow,
			obs:    obsAt(soon, "s", "opus", "a", 100, 0, 0, claudePrintOf(claudeOpts{noCache: true, messages: []string{"one", "two"}})),
			sess:   claudeLane("a", t0, 5*time.Minute),
			causes: []Cause{CauseNoBreakpoint},
		},
		{
			name: "lookback exceeded on partial append", kind: VerdictPartial, primary: CauseLookback, confidence: ConfidenceLow,
			obs:    obsAt(soon, "s", "opus", "a", 100, 3000, 0, claudePrintOf(claudeOpts{messages: messagesN(23)})),
			sess:   &session{id: "s", lanes: []lane{laneAt("opus", "a", t0, 10000, 5*time.Minute, claudePrintOf(claudeOpts{messages: messagesN(2)}))}},
			causes: []Cause{CauseLookback},
		},
		{
			name: "only the billing header changed", kind: VerdictMiss, primary: CauseVolatile, confidence: ConfidenceLow,
			obs:    obsAt(soon, "s", "opus", "a", 100, 0, 0, claudePrintOf(claudeOpts{cch: "other", messages: []string{"one", "two"}})),
			sess:   claudeLane("a", t0, 5*time.Minute),
			causes: []Cause{CauseVolatile},
		},
		{
			name: "codex cache key changed", kind: VerdictMiss, primary: CauseCacheKey, confidence: ConfidenceLow,
			obs:    obsAt(soon, "s", "gpt-5", "a", 300, 0, 0, codexPrintOf(codexOpts{cacheKey: "k2", input: []string{testInA}})),
			sess:   codexLane(t0),
			causes: []Cause{CauseCacheKey},
		},
		{
			name: "codex idle is a guess", kind: VerdictMiss, primary: CauseIdle, confidence: ConfidenceLow,
			obs:    obsAt(late, "s", "gpt-5", "a", 300, 0, 0, codexPrintOf(codexOpts{cacheKey: "k1", input: []string{testInA, testInB}})),
			sess:   codexLane(t0),
			causes: []Cause{CauseIdle},
		},
		{
			name: "codex instructions changed", kind: VerdictMiss, primary: CauseInstructions, confidence: ConfidenceHigh,
			obs:    obsAt(soon, "s", "gpt-5", "a", 10300, 0, 0, codexPrintOf(codexOpts{instructions: "be verbose", cacheKey: "k1", input: []string{testInA}})),
			sess:   codexLane(t0),
			causes: []Cause{CauseInstructions}, segment: prompt.SegmentRef{Kind: prompt.SegmentInstructions},
		},
		{
			name: "codex input changed", kind: VerdictMiss, primary: CauseMessage, confidence: ConfidenceHigh,
			obs:    obsAt(soon, "s", "gpt-5", "a", 10300, 0, 0, codexPrintOf(codexOpts{cacheKey: "k1", input: []string{testInB}})),
			sess:   codexLane(t0),
			causes: []Cause{CauseMessage}, segment: prompt.SegmentRef{Kind: prompt.SegmentInput, Index: 0},
		},
		{
			name: "codex unexplained", kind: VerdictMiss, primary: CauseUnexplained, confidence: ConfidenceLow,
			obs:    obsAt(soon, "s", "gpt-5", "a", 300, 0, 0, codexPrintOf(codexOpts{cacheKey: "k1", input: []string{testInA, testInB}})),
			sess:   codexLane(t0),
			causes: []Cause{CauseUnexplained},
		},
	}
	for _, tc := range cases {
		v, upd := judgeIn(tc.obs, tc.sess)
		if v.Kind != tc.kind || v.Primary != tc.primary || v.Confidence != tc.confidence {
			t.Errorf("%s: verdict = kind %v primary %v confidence %v (%+v)", tc.name, v.Kind, v.Primary, v.Confidence, v)
			continue
		}
		var want CauseSet
		for _, c := range tc.causes {
			want.Add(c)
		}
		if v.Causes != want {
			t.Errorf("%s: causes = %b, want %b", tc.name, v.Causes, want)
		}
		if got := v.Segment.unpack(); got != tc.segment {
			t.Errorf("%s: segment = %+v, want %+v", tc.name, got, tc.segment)
		}
		if v.ToolsFrom != tc.tools[0] || v.ToolsTo != tc.tools[1] {
			t.Errorf("%s: tools = %d -> %d", tc.name, v.ToolsFrom, v.ToolsTo)
		}
		if upd == nil {
			t.Errorf("%s: successful attempt must update the lane", tc.name)
		}
		if tc.name == "history truncated caps expected" && v.Expected != 2000 {
			t.Errorf("%s: expected = %d, want capped at the prompt total 2000", tc.name, v.Expected)
		}
	}
}

func TestSessionBaseline(t *testing.T) {
	var none *session
	p := claudePrintOf(claudeOpts{messages: []string{"hi"}})
	if base := none.baseline("opus", p); base != nil {
		t.Fatal("nil session must have no baseline")
	}
	sess := &session{id: "s", lanes: []lane{laneAt("haiku", "a", t0.Add(time.Minute), 1, 0, p), laneAt("opus", "a", t0, 2, 0, p)}}
	if base := sess.baseline("opus", p); base == nil || base.cachedPrefix != 2 {
		t.Fatalf("same-model lane = %+v", base)
	}
	if base := sess.baseline("sonnet", p); base != nil {
		t.Fatalf("another model's lane is never a baseline, got %+v", base)
	}
}

func TestSessionBaselinePicksLongestSharedPrefix(t *testing.T) {
	short := claudePrintOf(claudeOpts{messages: []string{"a", "b"}})
	long := claudePrintOf(claudeOpts{messages: []string{"x", "y", "z"}})
	other := claudePrintOf(claudeOpts{messages: []string{"x", "q"}})
	sess := &session{id: "s", lanes: []lane{
		laneAt("opus", "a", t0.Add(2*time.Minute), 1, 0, short), // most recent, shares only the preamble
		laneAt("opus", "a", t0, 2, 0, long),
		laneAt("opus", "a", t0.Add(time.Minute), 3, 0, other), // shares as much as long, more recent
	}}
	next := claudePrintOf(claudeOpts{messages: []string{"x", "y", "z", "w"}})
	if base := sess.baseline("opus", next); base == nil || base.cachedPrefix != 2 {
		t.Fatalf("the lane the request extends must win, got %+v", base)
	}
	tie := claudePrintOf(claudeOpts{messages: []string{"x", "n"}})
	if base := sess.baseline("opus", tie); base == nil || base.cachedPrefix != 3 {
		t.Fatalf("a tie must go to the most recently touched lane, got %+v", base)
	}
}
