// Package prices turns LiteLLM's model_prices_and_context_window.json into an
// immutable price table and looks up a price for (provider, model). Leaf package:
// no config import, no package state, no goroutines. The one network function
// (Fetch) is called by usagehistory's run loop in its own goroutine.
package prices

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Source says where a price came from.
type Source string

const (
	SourceBuiltin  Source = "builtin"  // go:embed snapshot
	SourceRemote   Source = "remote"   // daily LiteLLM fetch (cached in the usage DB)
	SourceOverride Source = "override" // config.yaml usage-history.prices.models
)

// Names of the rates that a fallback can fill. They are the values of Rates.Derived.
const (
	derivedInput        = "input"
	derivedOutput       = "output"
	derivedReasoning    = "reasoning"
	derivedCacheRead    = "cache_read"
	derivedCacheWrite5m = "cache_write_5m"
	derivedCacheWrite1h = "cache_write_1h"
)

// Rates are USD per token. After Decode every field is concrete: missing LiteLLM
// fields are filled by the fallback chain below and recorded in Derived, so cost
// code never branches on "missing".
//
// LiteLLM field -> rate, fallback when absent:
//
//	input_cost_per_token                         -> Input        (0 when absent)
//	output_cost_per_token                        -> Output       (0 when absent)
//	output_cost_per_reasoning_token              -> Reasoning    <- Output
//	cache_read_input_token_cost                  -> CacheRead    <- Input
//	cache_creation_input_token_cost              -> CacheWrite5m <- Input   (OpenAI-style: writes bill as input)
//	cache_creation_input_token_cost_above_1hr    -> CacheWrite1h <- CacheWrite5m
type Rates struct {
	Input        float64  `json:"input"`
	Output       float64  `json:"output"`
	Reasoning    float64  `json:"reasoning"`
	CacheRead    float64  `json:"cache_read"`
	CacheWrite5m float64  `json:"cache_write_5m"`
	CacheWrite1h float64  `json:"cache_write_1h"`
	Derived      []string `json:"derived,omitempty"` // names of rates filled by fallback
}

// Entry is one catalog model. Long holds the *_above_200k_tokens tier when LiteLLM
// has one (input_cost_per_token_above_200k_tokens, output_cost_per_token_above_200k_tokens,
// cache_read_input_token_cost_above_200k_tokens, cache_creation_input_token_cost_above_200k_tokens,
// cache_creation_input_token_cost_above_1hr_above_200k_tokens); each missing long field
// falls back to the standard rate of the same bucket. A standard rate that was itself
// derived stays chained in the long tier: a long cache read with no long field follows
// the long input, not the standard input.
type Entry struct {
	Std  Rates  `json:"std"`
	Long *Rates `json:"long,omitempty"`
}

// Catalog is an immutable set of entries keyed by lower-case LiteLLM key
// ("claude-opus-5-5", "xai/grok-4.7", "vertex_ai/gemini-3-pro").
type Catalog struct {
	Source    Source           `json:"source"`
	Generated time.Time        `json:"generated"` // when Trim produced it
	Entries   map[string]Entry `json:"entries"`
}

// The embedded snapshot is the OUTPUT of Trim, committed to the repo and
// refreshed by scripts/update-model-prices.sh (go run ./cmd/trim-prices < litellm.json
// > internal/usagehistory/prices/litellm_trimmed.json). Runtime fetches go through
// the same Trim, so there is one trimming path.
//
//go:embed litellm_trimmed.json
var embeddedSnapshot []byte

// Builtin decodes the embedded snapshot. It panics if the committed file is bad;
// TestBuiltinSnapshot keeps that from reaching a release.
func Builtin() *Catalog {
	c, err := Decode(embeddedSnapshot)
	if err != nil {
		panic(fmt.Sprintf("prices: embedded snapshot is invalid: %v", err))
	}
	return c
}

// minEntries guards against replacing a good catalog with a truncated or
// reshaped upstream file: a Trim result with fewer entries is rejected.
const minEntries = 200

// rawEntry holds the LiteLLM fields that Trim keeps. A field of the wrong JSON
// type fails the decode of that entry, and Trim drops the entry.
type rawEntry struct {
	Mode             string   `json:"mode"`
	Input            *float64 `json:"input_cost_per_token"`
	Output           *float64 `json:"output_cost_per_token"`
	Reasoning        *float64 `json:"output_cost_per_reasoning_token"`
	CacheRead        *float64 `json:"cache_read_input_token_cost"`
	CacheWrite5m     *float64 `json:"cache_creation_input_token_cost"`
	CacheWrite1h     *float64 `json:"cache_creation_input_token_cost_above_1hr"`
	InputLong        *float64 `json:"input_cost_per_token_above_200k_tokens"`
	OutputLong       *float64 `json:"output_cost_per_token_above_200k_tokens"`
	CacheReadLong    *float64 `json:"cache_read_input_token_cost_above_200k_tokens"`
	CacheWrite5mLong *float64 `json:"cache_creation_input_token_cost_above_200k_tokens"`
	CacheWrite1hLong *float64 `json:"cache_creation_input_token_cost_above_1hr_above_200k_tokens"`
}

func (r rawEntry) chatLike() bool {
	switch r.Mode {
	case "", "chat", "responses", "completion":
		return true
	}
	return false
}

func (r rawEntry) std() rateSet {
	return rateSet{r.Input, r.Output, r.Reasoning, r.CacheRead, r.CacheWrite5m, r.CacheWrite1h}
}

func (r rawEntry) long() rateSet {
	return rateSet{r.InputLong, r.OutputLong, nil, r.CacheReadLong, r.CacheWrite5mLong, r.CacheWrite1hLong}
}

// rateSet is the LiteLLM values of one tier. A nil field is absent.
type rateSet struct {
	input, output, reasoning, cacheRead, cacheWrite5m, cacheWrite1h *float64
}

func (s rateSet) any() bool {
	return s.input != nil || s.output != nil || s.reasoning != nil || s.cacheRead != nil ||
		s.cacheWrite5m != nil || s.cacheWrite1h != nil
}

func (s rateSet) hasNegative() bool {
	for _, p := range []*float64{s.input, s.output, s.reasoning, s.cacheRead, s.cacheWrite5m, s.cacheWrite1h} {
		if p != nil && *p < 0 {
			return true
		}
	}
	return false
}

// resolve fills every rate of one tier. base is the standard tier when s is the
// long tier, nil for the standard tier itself.
func (s rateSet) resolve(base *Rates) Rates {
	var r Rates
	var b Rates
	if base != nil {
		b = *base
	}
	// pick returns the value of a rate that may be absent. An absent rate follows
	// chain when there is no standard tier or the standard tier chained it too,
	// else it copies the standard rate.
	pick := func(name string, set *float64, baseValue float64, chain func() float64) float64 {
		if set != nil {
			return *set
		}
		r.Derived = append(r.Derived, name)
		if base == nil || b.derives(name) {
			return chain()
		}
		return baseValue
	}
	absent := func(name string, set *float64, baseValue float64) float64 {
		if set != nil {
			return *set
		}
		if base == nil {
			return 0
		}
		r.Derived = append(r.Derived, name)
		return baseValue
	}
	r.Input = absent(derivedInput, s.input, b.Input)
	r.Output = absent(derivedOutput, s.output, b.Output)
	r.Reasoning = pick(derivedReasoning, s.reasoning, b.Reasoning, func() float64 { return r.Output })
	r.CacheRead = pick(derivedCacheRead, s.cacheRead, b.CacheRead, func() float64 { return r.Input })
	r.CacheWrite5m = pick(derivedCacheWrite5m, s.cacheWrite5m, b.CacheWrite5m, func() float64 { return r.Input })
	r.CacheWrite1h = pick(derivedCacheWrite1h, s.cacheWrite1h, b.CacheWrite1h, func() float64 { return r.CacheWrite5m })
	return r
}

// derives reports whether the rate called name was filled by a fallback.
func (r Rates) derives(name string) bool {
	for _, d := range r.Derived {
		if d == name {
			return true
		}
	}
	return false
}

// Trim parses the full LiteLLM JSON and keeps, for every entry whose mode is
// chat/responses/completion (or absent) and that has an input_cost_per_token or an
// output_cost_per_token, only the fields listed on Rates/Entry, with fallbacks
// applied. Rejects results with fewer than minEntries. ~3 MB in, ~1 MB out.
func Trim(raw []byte, source Source, now time.Time) (*Catalog, error) {
	return trim(raw, source, now, minEntries)
}

func trim(raw []byte, source Source, now time.Time, min int) (*Catalog, error) {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, fmt.Errorf("prices: parse LiteLLM file: %w", err)
	}
	// Sorted keys make the choice between keys that differ only in case stable.
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	entries := make(map[string]Entry, len(all))
	for _, k := range keys {
		var re rawEntry
		if err := json.Unmarshal(all[k], &re); err != nil {
			continue
		}
		std := re.std()
		long := re.long()
		if !re.chatLike() || (std.input == nil && std.output == nil) || std.hasNegative() || long.hasNegative() {
			continue
		}
		key := strings.ToLower(k)
		if _, taken := entries[key]; taken && k != key {
			continue
		}
		e := Entry{Std: std.resolve(nil)}
		if long.any() {
			l := long.resolve(&e.Std)
			e.Long = &l
		}
		entries[key] = e
	}
	if len(entries) < min {
		return nil, fmt.Errorf("prices: LiteLLM file has %d usable entries, fewer than %d", len(entries), min)
	}
	return &Catalog{Source: source, Generated: now.UTC(), Entries: entries}, nil
}

// Encode/Decode are the trimmed format used for the embed and the DB cache.
// Encode writes one entry per line, in key order, so the committed snapshot
// diffs well and the output is the same for the same catalog.
func (c *Catalog) Encode() ([]byte, error) {
	keys := make([]string, 0, len(c.Entries))
	for k := range c.Entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var buf bytes.Buffer
	head, err := json.Marshal(struct {
		Source    Source    `json:"source"`
		Generated time.Time `json:"generated"`
	}{c.Source, c.Generated})
	if err != nil {
		return nil, fmt.Errorf("prices: encode catalog: %w", err)
	}
	buf.Write(bytes.TrimSuffix(head, []byte("}")))
	buf.WriteString(`,"entries":{`)
	for i, k := range keys {
		kb, errKey := json.Marshal(k)
		if errKey != nil {
			return nil, fmt.Errorf("prices: encode catalog: %w", errKey)
		}
		eb, errEntry := json.Marshal(c.Entries[k])
		if errEntry != nil {
			return nil, fmt.Errorf("prices: encode catalog: %w", errEntry)
		}
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.WriteByte('\n')
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(eb)
	}
	buf.WriteString("\n}}\n")
	return buf.Bytes(), nil
}

func Decode(body []byte) (*Catalog, error) {
	var c Catalog
	if err := json.Unmarshal(body, &c); err != nil {
		return nil, fmt.Errorf("prices: decode catalog: %w", err)
	}
	if len(c.Entries) == 0 {
		return nil, errors.New("prices: decode catalog: no entries")
	}
	return &c, nil
}
