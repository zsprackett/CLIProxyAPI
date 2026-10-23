// Package prices turns LiteLLM's model_prices_and_context_window.json into an
// immutable price table and looks up a price for (provider, model). Leaf package:
// no config import, no package state, no goroutines. The one network function
// (Fetch) is called by usagehistory's run loop in its own goroutine.
package prices

import (
	_ "embed"
	"errors"
	"time"
)

var errNotImplemented = errors.New("not implemented")

// Source says where a price came from.
type Source string

const (
	SourceBuiltin  Source = "builtin"  // go:embed snapshot
	SourceRemote   Source = "remote"   // daily LiteLLM fetch (cached in the usage DB)
	SourceOverride Source = "override" // config.yaml usage-history.prices.models
)

// Rates are USD per token. After Decode every field is concrete: missing LiteLLM
// fields are filled by the fallback chain below and recorded in Derived, so cost
// code never branches on "missing".
//
// LiteLLM field -> rate, fallback when absent:
//
//	input_cost_per_token                         -> Input        (required)
//	output_cost_per_token                        -> Output       (required)
//	output_cost_per_reasoning_token              -> Reasoning    <- Output
//	cache_read_input_token_cost                  -> CacheRead    <- Input
//	cache_creation_input_token_cost              -> CacheWrite5m <- Input   (OpenAI-style: writes bill as input)
//	cache_creation_input_token_cost_above_1hr    -> CacheWrite1h <- CacheWrite5m
type Rates struct {
	Input        float64 `json:"input"`
	Output       float64 `json:"output"`
	Reasoning    float64 `json:"reasoning"`
	CacheRead    float64 `json:"cache_read"`
	CacheWrite5m float64 `json:"cache_write_5m"`
	CacheWrite1h float64 `json:"cache_write_1h"`
	Derived      []string `json:"derived,omitempty"` // names of rates filled by fallback
}

// Entry is one catalog model. Long holds the *_above_200k_tokens tier when LiteLLM
// has one (input_cost_per_token_above_200k_tokens, output_cost_per_token_above_200k_tokens,
// cache_read_input_token_cost_above_200k_tokens, cache_creation_input_token_cost_above_200k_tokens,
// cache_creation_input_token_cost_above_1hr_above_200k_tokens); each missing long field
// falls back to the standard rate of the same bucket.
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

// Builtin decodes the embedded snapshot. Panics only in tests if the committed file is bad.
func Builtin() *Catalog { panic("not implemented") }

// minEntries guards against replacing a good catalog with a truncated or
// reshaped upstream file: a Trim result with fewer entries is rejected.
const minEntries = 200

// Trim parses the full LiteLLM JSON and keeps, for every entry whose mode is
// chat/responses/completion (or absent) and that has input_cost_per_token AND
// output_cost_per_token, only the fields listed on Rates/Entry, with fallbacks
// applied. Rejects results with fewer than minEntries. ~1.5 MB in, ~250 KB out.
func Trim(raw []byte, source Source, now time.Time) (*Catalog, error) { return nil, errNotImplemented }

// Encode/Decode are the trimmed format used for the embed and the DB cache.
func (c *Catalog) Encode() ([]byte, error)        { return nil, errNotImplemented }
func Decode(body []byte) (*Catalog, error)          { return nil, errNotImplemented }
