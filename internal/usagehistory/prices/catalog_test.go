package prices

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

// fixtureLiteLLM is a small excerpt in LiteLLM's shape, including entries Trim must drop.
const fixtureLiteLLM = `{
  "sample_spec": {"mode": "one of: chat, embedding", "input_cost_per_token": 0.0},
  "claude-opus-5-5": {
    "mode": "chat",
    "litellm_provider": "anthropic",
    "input_cost_per_token": 4e-06,
    "output_cost_per_token": 2e-05,
    "cache_read_input_token_cost": 2e-07,
    "cache_creation_input_token_cost": 5e-06,
    "cache_creation_input_token_cost_above_1hr": 8e-06,
    "input_cost_per_token_batches": 2e-06,
    "supports_vision": true
  },
  "claude-nocache": {
    "mode": "chat",
    "input_cost_per_token": 3e-06,
    "output_cost_per_token": 1.5e-05,
    "cache_creation_input_token_cost": 3.75e-06
  },
  "gpt-5": {
    "mode": "responses",
    "input_cost_per_token": 1.25e-06,
    "output_cost_per_token": 1e-05,
    "cache_read_input_token_cost": 1.25e-07
  },
  "o3-reasoning": {
    "mode": "chat",
    "input_cost_per_token": 2e-06,
    "output_cost_per_token": 8e-06,
    "output_cost_per_reasoning_token": 9e-06
  },
  "xai/grok-4.7": {
    "mode": "chat",
    "input_cost_per_token": 2e-06,
    "input_cost_per_token_above_200k_tokens": 4e-06,
    "output_cost_per_token": 6e-06,
    "output_cost_per_token_above_200k_tokens": 1.2e-05,
    "cache_read_input_token_cost": 5e-07,
    "cache_read_input_token_cost_above_200k_tokens": 1e-06
  },
  "vertex_ai/gemini-long": {
    "mode": "chat",
    "input_cost_per_token": 1e-06,
    "output_cost_per_token": 4e-06,
    "cache_read_input_token_cost": 1e-07,
    "cache_creation_input_token_cost": 2e-06,
    "cache_creation_input_token_cost_above_1hr": 3e-06,
    "input_cost_per_token_above_200k_tokens": 2e-06,
    "cache_creation_input_token_cost_above_200k_tokens": 4e-06,
    "cache_creation_input_token_cost_above_1hr_above_200k_tokens": 6e-06
  },
  "legacy-completion": {"mode": "completion", "input_cost_per_token": 1e-06, "output_cost_per_token": 2e-06},
  "no-mode": {"input_cost_per_token": 1e-06, "output_cost_per_token": 2e-06},
  "output-only": {"mode": "chat", "output_cost_per_token": 3e-06},
  "Mixed/Case-Model": {"mode": "chat", "input_cost_per_token": 1e-06, "output_cost_per_token": 2e-06},
  "text-embedding": {"mode": "embedding", "input_cost_per_token": 1e-07},
  "image-model": {"mode": "image_generation", "input_cost_per_token": 1e-07, "output_cost_per_token": 1e-07},
  "no-prices": {"mode": "chat", "max_tokens": 1000},
  "string-price": {"mode": "chat", "input_cost_per_token": "cheap", "output_cost_per_token": 1e-06},
  "negative-price": {"mode": "chat", "input_cost_per_token": -1e-06, "output_cost_per_token": 1e-06},
  "not-an-object": 7
}`

func TestTrim(t *testing.T) {
	cat, err := trim([]byte(fixtureLiteLLM), SourceBuiltin, testNow, 0)
	if err != nil {
		t.Fatalf("trim: %v", err)
	}
	if cat.Source != SourceBuiltin || !cat.Generated.Equal(testNow) {
		t.Fatalf("source/generated = %q/%v", cat.Source, cat.Generated)
	}

	tests := []struct {
		name     string
		key      string
		want     Entry
		wantKeep bool
	}{
		{
			name: "all fields present, only derived reasoning",
			key:  "claude-opus-5-5", wantKeep: true,
			want: Entry{Std: Rates{
				Input: 4e-06, Output: 2e-05, Reasoning: 2e-05, CacheRead: 2e-07,
				CacheWrite5m: 5e-06, CacheWrite1h: 8e-06, Derived: []string{"reasoning"},
			}},
		},
		{
			name: "1h write falls back to the 5m rate, cache read to input",
			key:  "claude-nocache", wantKeep: true,
			want: Entry{Std: Rates{
				Input: 3e-06, Output: 1.5e-05, Reasoning: 1.5e-05, CacheRead: 3e-06,
				CacheWrite5m: 3.75e-06, CacheWrite1h: 3.75e-06,
				Derived: []string{"reasoning", "cache_read", "cache_write_1h"},
			}},
		},
		{
			name: "responses mode kept, writes bill as input",
			key:  "gpt-5", wantKeep: true,
			want: Entry{Std: Rates{
				Input: 1.25e-06, Output: 1e-05, Reasoning: 1e-05, CacheRead: 1.25e-07,
				CacheWrite5m: 1.25e-06, CacheWrite1h: 1.25e-06,
				Derived: []string{"reasoning", "cache_write_5m", "cache_write_1h"},
			}},
		},
		{
			name: "explicit reasoning rate",
			key:  "o3-reasoning", wantKeep: true,
			want: Entry{Std: Rates{
				Input: 2e-06, Output: 8e-06, Reasoning: 9e-06, CacheRead: 2e-06,
				CacheWrite5m: 2e-06, CacheWrite1h: 2e-06,
				Derived: []string{"cache_read", "cache_write_5m", "cache_write_1h"},
			}},
		},
		{
			name: "above-200k tier: unset long cache writes follow the long input",
			key:  "xai/grok-4.7", wantKeep: true,
			want: Entry{
				Std: Rates{
					Input: 2e-06, Output: 6e-06, Reasoning: 6e-06, CacheRead: 5e-07,
					CacheWrite5m: 2e-06, CacheWrite1h: 2e-06,
					Derived: []string{"reasoning", "cache_write_5m", "cache_write_1h"},
				},
				Long: &Rates{
					Input: 4e-06, Output: 1.2e-05, Reasoning: 1.2e-05, CacheRead: 1e-06,
					CacheWrite5m: 4e-06, CacheWrite1h: 4e-06,
					Derived: []string{"reasoning", "cache_write_5m", "cache_write_1h"},
				},
			},
		},
		{
			name: "above-200k tier: missing long fields copy the standard rate",
			key:  "vertex_ai/gemini-long", wantKeep: true,
			want: Entry{
				Std: Rates{
					Input: 1e-06, Output: 4e-06, Reasoning: 4e-06, CacheRead: 1e-07,
					CacheWrite5m: 2e-06, CacheWrite1h: 3e-06, Derived: []string{"reasoning"},
				},
				Long: &Rates{
					Input: 2e-06, Output: 4e-06, Reasoning: 4e-06, CacheRead: 1e-07,
					CacheWrite5m: 4e-06, CacheWrite1h: 6e-06,
					Derived: []string{"output", "reasoning", "cache_read"},
				},
			},
		},
		{
			name: "completion mode kept",
			key:  "legacy-completion", wantKeep: true,
			want: Entry{Std: Rates{
				Input: 1e-06, Output: 2e-06, Reasoning: 2e-06, CacheRead: 1e-06,
				CacheWrite5m: 1e-06, CacheWrite1h: 1e-06,
				Derived: []string{"reasoning", "cache_read", "cache_write_5m", "cache_write_1h"},
			}},
		},
		{
			name: "absent mode kept",
			key:  "no-mode", wantKeep: true,
			want: Entry{Std: Rates{
				Input: 1e-06, Output: 2e-06, Reasoning: 2e-06, CacheRead: 1e-06,
				CacheWrite5m: 1e-06, CacheWrite1h: 1e-06,
				Derived: []string{"reasoning", "cache_read", "cache_write_5m", "cache_write_1h"},
			}},
		},
		{
			name: "output price alone is enough, input is zero",
			key:  "output-only", wantKeep: true,
			want: Entry{Std: Rates{
				Output: 3e-06, Reasoning: 3e-06,
				Derived: []string{"reasoning", "cache_read", "cache_write_5m", "cache_write_1h"},
			}},
		},
		{
			name: "key is lower-cased",
			key:  "mixed/case-model", wantKeep: true,
			want: Entry{Std: Rates{
				Input: 1e-06, Output: 2e-06, Reasoning: 2e-06, CacheRead: 1e-06,
				CacheWrite5m: 1e-06, CacheWrite1h: 1e-06,
				Derived: []string{"reasoning", "cache_read", "cache_write_5m", "cache_write_1h"},
			}},
		},
		{name: "sample_spec dropped", key: "sample_spec"},
		{name: "embedding dropped", key: "text-embedding"},
		{name: "image generation dropped", key: "image-model"},
		{name: "no price dropped", key: "no-prices"},
		{name: "non-numeric price dropped", key: "string-price"},
		{name: "negative price dropped", key: "negative-price"},
		{name: "non-object dropped", key: "not-an-object"},
		{name: "original case is not a key", key: "Mixed/Case-Model"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := cat.Entries[tt.key]
			if ok != tt.wantKeep {
				t.Fatalf("kept = %v, want %v", ok, tt.wantKeep)
			}
			if tt.wantKeep && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("entry mismatch\n got %+v long=%+v\nwant %+v long=%+v", got.Std, got.Long, tt.want.Std, tt.want.Long)
			}
		})
	}
	if len(cat.Entries) != 10 {
		t.Errorf("len(Entries) = %d, want 10", len(cat.Entries))
	}
}

func TestTrimRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"not json", `<html>`, "parse"},
		{"not an object", `[1,2]`, "parse"},
		{"too few entries", fixtureLiteLLM, "fewer than"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Trim([]byte(tt.raw), SourceRemote, testNow)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cat, err := trim([]byte(fixtureLiteLLM), SourceRemote, testNow, 0)
	if err != nil {
		t.Fatal(err)
	}
	body, err := cat.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	again, err := cat.Encode()
	if err != nil || !bytes.Equal(body, again) {
		t.Fatalf("Encode is not deterministic (err=%v)", err)
	}
	got, err := Decode(body)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Source != SourceRemote || !got.Generated.Equal(testNow) || !reflect.DeepEqual(got.Entries, cat.Entries) {
		t.Fatalf("round trip changed the catalog")
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	for name, body := range map[string]string{
		"empty":      ``,
		"not json":   `nope`,
		"no entries": `{"source":"remote"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Decode([]byte(body)); err == nil {
				t.Fatal("want error")
			}
		})
	}
}
