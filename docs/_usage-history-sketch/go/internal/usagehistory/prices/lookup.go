package prices

// Override is one config.yaml price override, converted by usagehistory from
// config.ModelPriceOverride. Rates are USD per MILLION tokens in config (human
// scale) and converted to per-token here. A nil rate means "inherit".
type Override struct {
	Model    string // exact Record.Model, case-insensitive
	Provider string // optional: only for this provider (executor id)
	SameAs   string // optional: inherit every rate from this catalog key first
	PerMTok  OverrideRates
}

// OverrideRates are optional per-million rates.
type OverrideRates struct {
	Input, Output, Reasoning, CacheRead, CacheWrite5m, CacheWrite1h *float64
}

// Price is a resolved price for one (provider, model).
type Price struct {
	Matched string // the catalog key, or "override:<model>", that supplied it
	Source  Source
	Std     Rates
	Long    *Rates // nil: no long-context tier, Std applies to every request
}

// RatesFor returns the rates for a rollup row's tier (Long when longCtx and present).
func (p Price) RatesFor(longCtx bool) Rates { panic("not implemented") }

// Table is an immutable (catalog, overrides) pair resolved for lookup. The
// usagehistory Service swaps a *Table atomically when the catalog is refreshed
// or the overrides in config change; a report loads it once and memoizes
// Lookup per (provider, model) in a local map.
type Table struct {
	catalog   *Catalog
	overrides map[overrideKey]Price // resolved at build time
}

type overrideKey struct{ provider, model string } // provider "" = any

// NewTable resolves overrides against the catalog. Invalid overrides are
// skipped and returned as errors for a log line (a config reload must not fail):
// empty model; SameAs that matches nothing AND no Input+Output given; negative rate.
//
// Override resolution (pseudocode):
//
//	var base Price
//	switch {
//	case o.SameAs != "":          base = catalogLookup(o.Provider, o.SameAs)  // rules 3-4; miss -> needs Input+Output
//	case catalog has o.Model:     base = catalogLookup(o.Provider, o.Model)   // partial override, e.g. only cache_write_1h
//	default:                      base = Price{}                              // full manual price; Input+Output required
//	}
//	for each non-nil PerMTok field f: base.Std.f = v/1e6; if base.Long != nil { base.Long.f = v/1e6 }
//	  // an explicit override rate applies to both tiers: overriding means "this is what I pay"
//	re-apply the fallback chain to fields that were Derived and are still unset by the override
//	base.Source = SourceOverride; base.Matched = "override:" + o.Model (+ " same-as " + o.SameAs)
//	overrides[{lower(o.Provider), lower(o.Model)}] = base
func NewTable(c *Catalog, overrides []Override) (*Table, []error) { return nil, []error{errNotImplemented} }

// Lookup finds the price for a rollup row's (provider, model). First hit wins:
//
//  1. override {provider, model}
//  2. override {"", model}
//  3. catalog[lower(model)]                                  bare key: "claude-opus-5-5", or an
//                                                            already-prefixed id like "xai/grok-4.7"
//  4. catalog[p + "/" + lower(model)] for p in providerPrefixes[provider]
//                                                            "xai/grok-4.7", "vertex_ai/gemini-3-pro"
//
// No fuzzy matching (no date stripping, no "-thinking" stripping): a silently
// wrong price is worse than an "unpriced" line telling Zac to add a same-as
// override. Antigravity ("claude-opus-4-6-thinking") and Devin ("devin/...") ids
// land in Report.UnpricedModels until an override maps them.
func (t *Table) Lookup(provider, model string) (Price, bool) { return Price{}, false }

// providerPrefixes maps executor ids to LiteLLM key prefixes tried by rule 4.
// openai-compatible-<name> has none: its models are arbitrary, overrides only.
var providerPrefixes = map[string][]string{
	"claude":      {"anthropic"},
	"codex":       {"openai"},
	"xai":         {"xai"},
	"gemini":      {"gemini"},
	"aistudio":    {"gemini"},
	"vertex":      {"vertex_ai"},
	"antigravity": {"gemini", "vertex_ai"},
	"kimi":        {"moonshot"},
}

// Catalog returns the catalog behind the table (source, generation time) for report footers.
func (t *Table) Catalog() *Catalog { return t.catalog }
