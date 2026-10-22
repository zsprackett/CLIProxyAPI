package prices

import (
	"reflect"
	"strings"
	"testing"
)

const lookupFixture = `{
  "claude-opus-5-5": {"mode": "chat", "input_cost_per_token": 4e-06, "output_cost_per_token": 2e-05,
    "cache_read_input_token_cost": 2e-07, "cache_creation_input_token_cost": 5e-06,
    "cache_creation_input_token_cost_above_1hr": 8e-06},
  "claude-opus-4-6": {"mode": "chat", "input_cost_per_token": 5e-06, "output_cost_per_token": 2.5e-05,
    "cache_read_input_token_cost": 5e-07, "cache_creation_input_token_cost": 6e-06,
    "cache_creation_input_token_cost_above_1hr": 1e-05},
  "xai/grok-4.7": {"mode": "chat", "input_cost_per_token": 2e-06, "output_cost_per_token": 6e-06,
    "cache_read_input_token_cost": 5e-07,
    "input_cost_per_token_above_200k_tokens": 4e-06, "output_cost_per_token_above_200k_tokens": 1.2e-05,
    "cache_read_input_token_cost_above_200k_tokens": 1e-06},
  "gpt-5": {"mode": "responses", "input_cost_per_token": 1e-06, "output_cost_per_token": 8e-06,
    "cache_read_input_token_cost": 1e-07},
  "vertex_ai/gemini-3-pro": {"mode": "chat", "input_cost_per_token": 2e-06, "output_cost_per_token": 1.2e-05},
  "gemini/gemini-3-pro": {"mode": "chat", "input_cost_per_token": 3e-06, "output_cost_per_token": 1.3e-05},
  "vertex_ai/only-vertex": {"mode": "chat", "input_cost_per_token": 1e-06, "output_cost_per_token": 2e-06},
  "anthropic/claude-haiku-x": {"mode": "chat", "input_cost_per_token": 1e-06, "output_cost_per_token": 5e-06},
  "openai/gpt-prefixed": {"mode": "chat", "input_cost_per_token": 1e-06, "output_cost_per_token": 5e-06},
  "moonshot/kimi-k3": {"mode": "chat", "input_cost_per_token": 1e-06, "output_cost_per_token": 3e-06},
  "foo": {"mode": "chat", "input_cost_per_token": 1e-06, "output_cost_per_token": 1e-06},
  "xai/foo": {"mode": "chat", "input_cost_per_token": 9e-06, "output_cost_per_token": 9e-06}
}`

func testCatalog(t *testing.T) *Catalog {
	t.Helper()
	cat, err := trim([]byte(lookupFixture), SourceRemote, testNow, 0)
	if err != nil {
		t.Fatalf("trim fixture: %v", err)
	}
	return cat
}

func newTestTable(t *testing.T, overrides ...Override) *Table {
	t.Helper()
	tbl, errs := NewTable(testCatalog(t), overrides)
	if len(errs) != 0 {
		t.Fatalf("NewTable errors: %v", errs)
	}
	return tbl
}

func ptr(v float64) *float64 { return &v }

func TestLookupCatalogOrder(t *testing.T) {
	tbl := newTestTable(t)
	tests := []struct {
		name         string
		provider     string
		model        string
		wantMatched  string // empty: no price
		wantInputPer float64
	}{
		{"bare key", "claude", "claude-opus-5-5", "claude-opus-5-5", 4e-06},
		{"case-insensitive model and provider", "CLAUDE", "Claude-Opus-5-5", "claude-opus-5-5", 4e-06},
		{"already prefixed id is a bare key", "xai", "xai/grok-4.7", "xai/grok-4.7", 2e-06},
		{"xai prefix", "xai", "grok-4.7", "xai/grok-4.7", 2e-06},
		{"vertex prefix", "vertex", "gemini-3-pro", "vertex_ai/gemini-3-pro", 2e-06},
		{"gemini prefix", "gemini", "gemini-3-pro", "gemini/gemini-3-pro", 3e-06},
		{"aistudio uses the gemini prefix", "aistudio", "gemini-3-pro", "gemini/gemini-3-pro", 3e-06},
		{"antigravity tries gemini first", "antigravity", "gemini-3-pro", "gemini/gemini-3-pro", 3e-06},
		{"antigravity falls through to vertex_ai", "antigravity", "only-vertex", "vertex_ai/only-vertex", 1e-06},
		{"claude uses the anthropic prefix", "claude", "claude-haiku-x", "anthropic/claude-haiku-x", 1e-06},
		{"codex uses the openai prefix", "codex", "gpt-prefixed", "openai/gpt-prefixed", 1e-06},
		{"kimi uses the moonshot prefix", "kimi", "kimi-k3", "moonshot/kimi-k3", 1e-06},
		{"bare key beats prefixed key", "xai", "foo", "foo", 1e-06},
		{"prefix of another provider is not tried", "vertex", "grok-4.7", "", 0},
		{"no date stripping", "claude", "claude-opus-5-5-20260101", "", 0},
		{"no thinking-suffix stripping", "antigravity", "claude-opus-4-6-thinking", "", 0},
		{"devin has no prefix", "devin", "grok-4.7", "", 0},
		{"openai-compatible has no prefix", "openai-compatible-acme", "grok-4.7", "", 0},
		{"empty model", "claude", "", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tbl.Lookup(tt.provider, tt.model)
			if ok != (tt.wantMatched != "") {
				t.Fatalf("found = %v (%+v), want %v", ok, got, tt.wantMatched != "")
			}
			if !ok {
				return
			}
			if got.Matched != tt.wantMatched || got.Std.Input != tt.wantInputPer || got.Source != SourceRemote {
				t.Errorf("got matched=%q input=%v source=%q, want matched=%q input=%v source=%q",
					got.Matched, got.Std.Input, got.Source, tt.wantMatched, tt.wantInputPer, SourceRemote)
			}
		})
	}
}

func TestLookupOverrideOrder(t *testing.T) {
	tbl := newTestTable(t,
		Override{Model: "claude-opus-5-5", Provider: "claude", PerMTok: OverrideRates{Input: ptr(1)}},
		Override{Model: "Claude-Opus-5-5", PerMTok: OverrideRates{Input: ptr(2)}},
		Override{Model: "shared-model", PerMTok: OverrideRates{Input: ptr(3), Output: ptr(4)}},
	)
	tests := []struct {
		name      string
		provider  string
		model     string
		wantInput float64
		wantMatch string
	}{
		{"provider+model override first", "claude", "claude-opus-5-5", 1e-06, "override:claude-opus-5-5"},
		{"model override for another provider", "antigravity", "claude-opus-5-5", 2e-06, "override:Claude-Opus-5-5"},
		{"model override for any provider", "devin", "shared-model", 3e-06, "override:shared-model"},
		{"override wins over catalog", "claude", "CLAUDE-OPUS-5-5", 1e-06, "override:claude-opus-5-5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := tbl.Lookup(tt.provider, tt.model)
			if !ok {
				t.Fatal("no price")
			}
			if got.Std.Input != tt.wantInput || got.Matched != tt.wantMatch || got.Source != SourceOverride {
				t.Errorf("got input=%v matched=%q source=%q", got.Std.Input, got.Matched, got.Source)
			}
		})
	}
}

func TestOverrideResolution(t *testing.T) {
	opus := Rates{Input: 4e-06, Output: 2e-05, Reasoning: 2e-05, CacheRead: 2e-07, CacheWrite5m: 5e-06, CacheWrite1h: 8e-06}
	tests := []struct {
		name      string
		override  Override
		provider  string
		model     string
		wantStd   Rates
		wantLong  *Rates
		wantDeriv []string
		wantMatch string
	}{
		{
			name:      "same-as inherits every rate",
			override:  Override{Model: "claude-opus-4-6-thinking", Provider: "antigravity", SameAs: "claude-opus-5-5"},
			provider:  "antigravity",
			model:     "claude-opus-4-6-thinking",
			wantStd:   opus,
			wantDeriv: []string{"reasoning"},
			wantMatch: "override:claude-opus-4-6-thinking same-as claude-opus-5-5",
		},
		{
			name:      "same-as resolves through the provider prefix",
			override:  Override{Model: "grok-fast", Provider: "xai", SameAs: "grok-4.7"},
			provider:  "xai",
			model:     "grok-fast",
			wantStd:   Rates{Input: 2e-06, Output: 6e-06, Reasoning: 6e-06, CacheRead: 5e-07, CacheWrite5m: 2e-06, CacheWrite1h: 2e-06},
			wantLong:  &Rates{Input: 4e-06, Output: 1.2e-05, Reasoning: 1.2e-05, CacheRead: 1e-06, CacheWrite5m: 4e-06, CacheWrite1h: 4e-06},
			wantDeriv: []string{"reasoning", "cache_write_5m", "cache_write_1h"},
			wantMatch: "override:grok-fast same-as grok-4.7",
		},
		{
			name:      "partial override changes one rate and keeps the rest",
			override:  Override{Model: "claude-opus-5-5", PerMTok: OverrideRates{CacheWrite1h: ptr(10)}},
			provider:  "claude",
			model:     "claude-opus-5-5",
			wantStd:   Rates{Input: 4e-06, Output: 2e-05, Reasoning: 2e-05, CacheRead: 2e-07, CacheWrite5m: 5e-06, CacheWrite1h: 1e-05},
			wantDeriv: []string{"reasoning"},
			wantMatch: "override:claude-opus-5-5",
		},
		{
			name:      "same-as plus explicit rate: the explicit rate wins",
			override:  Override{Model: "claude-opus-4-6-thinking", SameAs: "claude-opus-4-6", PerMTok: OverrideRates{Output: ptr(30)}},
			provider:  "antigravity",
			model:     "claude-opus-4-6-thinking",
			wantStd:   Rates{Input: 5e-06, Output: 3e-05, Reasoning: 3e-05, CacheRead: 5e-07, CacheWrite5m: 6e-06, CacheWrite1h: 1e-05},
			wantDeriv: []string{"reasoning"},
			wantMatch: "override:claude-opus-4-6-thinking same-as claude-opus-4-6",
		},
		{
			name:      "overriding input re-derives the rates that followed it",
			override:  Override{Model: "gpt-5", PerMTok: OverrideRates{Input: ptr(5)}},
			provider:  "codex",
			model:     "gpt-5",
			wantStd:   Rates{Input: 5e-06, Output: 8e-06, Reasoning: 8e-06, CacheRead: 1e-07, CacheWrite5m: 5e-06, CacheWrite1h: 5e-06},
			wantDeriv: []string{"reasoning", "cache_write_5m", "cache_write_1h"},
			wantMatch: "override:gpt-5",
		},
		{
			name:      "an overridden rate leaves the derived list",
			override:  Override{Model: "gpt-5", PerMTok: OverrideRates{Reasoning: ptr(12)}},
			provider:  "codex",
			model:     "gpt-5",
			wantStd:   Rates{Input: 1e-06, Output: 8e-06, Reasoning: 1.2e-05, CacheRead: 1e-07, CacheWrite5m: 1e-06, CacheWrite1h: 1e-06},
			wantDeriv: []string{"cache_write_5m", "cache_write_1h"},
			wantMatch: "override:gpt-5",
		},
		{
			name:     "an explicit rate applies to both tiers and re-chains the long ones",
			override: Override{Model: "grok-4.7", Provider: "xai", PerMTok: OverrideRates{Input: ptr(3)}},
			provider: "xai",
			model:    "grok-4.7",
			wantStd:  Rates{Input: 3e-06, Output: 6e-06, Reasoning: 6e-06, CacheRead: 5e-07, CacheWrite5m: 3e-06, CacheWrite1h: 3e-06},
			wantLong: &Rates{Input: 3e-06, Output: 1.2e-05, Reasoning: 1.2e-05, CacheRead: 1e-06, CacheWrite5m: 3e-06, CacheWrite1h: 3e-06},
			wantDeriv: []string{
				"reasoning", "cache_write_5m", "cache_write_1h",
			},
			wantMatch: "override:grok-4.7",
		},
		{
			name:      "unknown model with input and output only: the rest follows",
			override:  Override{Model: "my-model", PerMTok: OverrideRates{Input: ptr(1), Output: ptr(2)}},
			provider:  "openai-compatible-acme",
			model:     "my-model",
			wantStd:   Rates{Input: 1e-06, Output: 2e-06, Reasoning: 2e-06, CacheRead: 1e-06, CacheWrite5m: 1e-06, CacheWrite1h: 1e-06},
			wantDeriv: []string{"reasoning", "cache_read", "cache_write_5m", "cache_write_1h"},
			wantMatch: "override:my-model",
		},
		{
			name:      "free local model",
			override:  Override{Model: "my-local-model", PerMTok: OverrideRates{Input: ptr(0), Output: ptr(0)}},
			provider:  "openai-compatible-local",
			model:     "my-local-model",
			wantStd:   Rates{},
			wantDeriv: []string{"reasoning", "cache_read", "cache_write_5m", "cache_write_1h"},
			wantMatch: "override:my-local-model",
		},
		{
			name:      "same-as that matches nothing works with explicit input and output",
			override:  Override{Model: "devin/x", SameAs: "no-such-key", PerMTok: OverrideRates{Input: ptr(2), Output: ptr(4), CacheRead: ptr(1)}},
			provider:  "devin",
			model:     "devin/x",
			wantStd:   Rates{Input: 2e-06, Output: 4e-06, Reasoning: 4e-06, CacheRead: 1e-06, CacheWrite5m: 2e-06, CacheWrite1h: 2e-06},
			wantDeriv: []string{"reasoning", "cache_write_5m", "cache_write_1h"},
			wantMatch: "override:devin/x same-as no-such-key",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tbl := newTestTable(t, tt.override)
			got, ok := tbl.Lookup(tt.provider, tt.model)
			if !ok {
				t.Fatal("no price")
			}
			if !sameRates(got.Std, tt.wantStd) {
				t.Errorf("Std = %+v, want %+v", got.Std, tt.wantStd)
			}
			if !reflect.DeepEqual(got.Std.Derived, tt.wantDeriv) {
				t.Errorf("Std.Derived = %v, want %v", got.Std.Derived, tt.wantDeriv)
			}
			switch {
			case tt.wantLong == nil && got.Long != nil:
				t.Errorf("Long = %+v, want none", *got.Long)
			case tt.wantLong != nil && got.Long == nil:
				t.Error("Long is missing")
			case tt.wantLong != nil && !sameRates(*got.Long, *tt.wantLong):
				t.Errorf("Long = %+v, want %+v", *got.Long, *tt.wantLong)
			}
			if got.Matched != tt.wantMatch || got.Source != SourceOverride {
				t.Errorf("matched=%q source=%q, want %q/%q", got.Matched, got.Source, tt.wantMatch, SourceOverride)
			}
		})
	}
}

func TestOverrideDoesNotChangeCatalog(t *testing.T) {
	cat := testCatalog(t)
	before, err := cat.Encode()
	if err != nil {
		t.Fatal(err)
	}
	_, errs := NewTable(cat, []Override{
		{Model: "grok-4.7", Provider: "xai", PerMTok: OverrideRates{Input: ptr(3), CacheWrite1h: ptr(9)}},
		{Model: "gpt-5", PerMTok: OverrideRates{Input: ptr(7)}},
	})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	after, err := cat.Encode()
	if err != nil || string(before) != string(after) {
		t.Fatalf("an override changed the catalog (err=%v)", err)
	}
}

func TestNewTableSkipsInvalidOverrides(t *testing.T) {
	good := Override{Model: "good-model", PerMTok: OverrideRates{Input: ptr(1), Output: ptr(2)}}
	tests := []struct {
		name    string
		bad     Override
		wantErr string
	}{
		{"empty model", Override{Model: "  ", PerMTok: OverrideRates{Input: ptr(1), Output: ptr(2)}}, "model"},
		{"unknown model without rates", Override{Model: "mystery"}, "input and output"},
		{"unknown model with only input", Override{Model: "mystery", PerMTok: OverrideRates{Input: ptr(1)}}, "input and output"},
		{"same-as matches nothing and no rates", Override{Model: "x", SameAs: "no-such-key"}, "same-as"},
		{"negative rate", Override{Model: "claude-opus-5-5", PerMTok: OverrideRates{Input: ptr(-1)}}, "negative"},
		{"duplicate of an earlier override", Override{Model: "good-model", PerMTok: OverrideRates{Input: ptr(9), Output: ptr(9)}}, "duplicate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tbl, errs := NewTable(testCatalog(t), []Override{good, tt.bad})
			if len(errs) != 1 || !strings.Contains(errs[0].Error(), tt.wantErr) {
				t.Fatalf("errs = %v, want one containing %q", errs, tt.wantErr)
			}
			got, ok := tbl.Lookup("claude", "good-model")
			if !ok || got.Std.Input != 1e-06 {
				t.Errorf("the valid override was lost: %+v ok=%v", got, ok)
			}
			if _, ok := tbl.Lookup("claude", "mystery"); ok {
				t.Error("an invalid override still priced a model")
			}
		})
	}
}

func TestNewTableNilCatalog(t *testing.T) {
	tbl, errs := NewTable(nil, []Override{{Model: "m", PerMTok: OverrideRates{Input: ptr(1), Output: ptr(2)}}})
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	if _, ok := tbl.Lookup("claude", "claude-opus-5-5"); ok {
		t.Error("nil catalog priced a model")
	}
	if _, ok := tbl.Lookup("claude", "m"); !ok {
		t.Error("override lost with a nil catalog")
	}
}

func TestPriceRatesFor(t *testing.T) {
	std := Rates{Input: 1}
	long := Rates{Input: 2}
	tests := []struct {
		name    string
		price   Price
		longCtx bool
		want    float64
	}{
		{"std tier", Price{Std: std, Long: &long}, false, 1},
		{"long tier", Price{Std: std, Long: &long}, true, 2},
		{"no long tier: std applies to long prompts", Price{Std: std}, true, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.price.RatesFor(tt.longCtx).Input; got != tt.want {
				t.Errorf("Input = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTableCatalog(t *testing.T) {
	cat := testCatalog(t)
	tbl, _ := NewTable(cat, nil)
	if tbl.Catalog() != cat {
		t.Error("Catalog() is not the catalog the table was built from")
	}
}
