package prices

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

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

// Price is a resolved price for one (provider, model). A Price that comes from the
// catalog shares its Derived slices and Long pointer with the catalog: do not modify them.
type Price struct {
	Matched string // the catalog key, or "override:<model>", that supplied it
	Source  Source
	Std     Rates
	Long    *Rates // nil: no long-context tier, Std applies to every request
}

// RatesFor returns the rates for a rollup row's tier (Long when longCtx and present).
func (p Price) RatesFor(longCtx bool) Rates {
	if longCtx && p.Long != nil {
		return *p.Long
	}
	return p.Std
}

// Table is an immutable (catalog, overrides) pair resolved for lookup. The
// usagehistory Service swaps a *Table atomically when the catalog is refreshed
// or the overrides in config change; a report loads it once and memoizes
// Lookup per (provider, model) in a local map.
type Table struct {
	catalog   *Catalog
	overrides map[overrideKey]Price // resolved at build time
}

type overrideKey struct{ provider, model string } // provider "" = any

// derivedChain lists the rates a fallback can fill, in the order that resolves them:
// each chain source comes before the rate that follows it.
var derivedChain = []string{derivedInput, derivedOutput, derivedReasoning, derivedCacheRead, derivedCacheWrite5m, derivedCacheWrite1h}

func norm(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// NewTable resolves overrides against the catalog. Invalid overrides are
// skipped and returned as errors for a log line (a config reload must not fail):
// empty model; SameAs that matches nothing AND no Input+Output given; negative rate;
// a second override for the same (provider, model).
//
// Override resolution:
//
//	SameAs set       base = catalog price of SameAs (rules 3-4); a miss needs Input+Output
//	model in catalog base = catalog price of Model (partial override, e.g. only cache_write_1h)
//	otherwise        base = empty price; Input+Output are required
//
// Each non-nil rate replaces the base rate in both tiers: overriding means "this is
// what I pay". Rates that were derived and are not overridden follow their chain again.
func NewTable(c *Catalog, overrides []Override) (*Table, []error) {
	if c == nil {
		c = &Catalog{Entries: map[string]Entry{}}
	}
	t := &Table{catalog: c, overrides: make(map[overrideKey]Price, len(overrides))}
	var errs []error
	for i, o := range overrides {
		p, err := t.resolveOverride(o)
		if err != nil {
			errs = append(errs, fmt.Errorf("price override %d (model %q): %w", i+1, o.Model, err))
			continue
		}
		key := overrideKey{norm(o.Provider), norm(o.Model)}
		if _, dup := t.overrides[key]; dup {
			errs = append(errs, fmt.Errorf("price override %d (model %q): duplicate of an earlier override", i+1, o.Model))
			continue
		}
		t.overrides[key] = p
	}
	return t, errs
}

func (t *Table) resolveOverride(o Override) (Price, error) {
	model := strings.TrimSpace(o.Model)
	if model == "" {
		return Price{}, errors.New("model is empty")
	}
	if o.PerMTok.hasNegative() {
		return Price{}, errors.New("negative rate")
	}
	lookupKey := model
	if o.SameAs != "" {
		lookupKey = o.SameAs
	}
	base, found := t.catalogLookup(norm(o.Provider), norm(lookupKey))
	if !found && (o.PerMTok.Input == nil || o.PerMTok.Output == nil) {
		if o.SameAs != "" {
			return Price{}, fmt.Errorf("same-as %q matches nothing: set input and output", o.SameAs)
		}
		return Price{}, errors.New("no catalog price for this model: set input and output")
	}
	if found {
		base = clonePrice(base)
	} else {
		base = Price{Std: Rates{Derived: slices.Clone(derivedChain[2:])}}
	}

	applyOverride(&base.Std, o.PerMTok, nil)
	if base.Long != nil {
		applyOverride(base.Long, o.PerMTok, &base.Std)
	}
	base.Source = SourceOverride
	base.Matched = "override:" + model
	if o.SameAs != "" {
		base.Matched += " same-as " + o.SameAs
	}
	return base, nil
}

func (o OverrideRates) hasNegative() bool {
	for _, p := range []*float64{o.Input, o.Output, o.Reasoning, o.CacheRead, o.CacheWrite5m, o.CacheWrite1h} {
		if p != nil && *p < 0 {
			return true
		}
	}
	return false
}

func clonePrice(p Price) Price {
	p.Std.Derived = slices.Clone(p.Std.Derived)
	if p.Long != nil {
		l := *p.Long
		l.Derived = slices.Clone(l.Derived)
		p.Long = &l
	}
	return p
}

func (r *Rates) field(name string) *float64 {
	switch name {
	case derivedInput:
		return &r.Input
	case derivedOutput:
		return &r.Output
	case derivedReasoning:
		return &r.Reasoning
	case derivedCacheRead:
		return &r.CacheRead
	case derivedCacheWrite5m:
		return &r.CacheWrite5m
	default:
		return &r.CacheWrite1h
	}
}

// applyOverride sets the explicit rates (USD per million tokens) on r, then
// re-resolves the derived rates that the override did not set. std is the standard
// tier when r is the long tier.
func applyOverride(r *Rates, o OverrideRates, std *Rates) {
	explicit := map[string]*float64{
		derivedInput: o.Input, derivedOutput: o.Output, derivedReasoning: o.Reasoning,
		derivedCacheRead: o.CacheRead, derivedCacheWrite5m: o.CacheWrite5m, derivedCacheWrite1h: o.CacheWrite1h,
	}
	for _, name := range derivedChain {
		if v := explicit[name]; v != nil {
			*r.field(name) = *v / 1e6
			r.Derived = slices.DeleteFunc(r.Derived, func(d string) bool { return d == name })
		}
	}
	for _, name := range derivedChain {
		if !r.derives(name) {
			continue
		}
		switch {
		case std != nil && !std.derives(name):
			*r.field(name) = *std.field(name)
		case name == derivedReasoning:
			r.Reasoning = r.Output
		case name == derivedCacheRead, name == derivedCacheWrite5m:
			*r.field(name) = r.Input
		case name == derivedCacheWrite1h:
			r.CacheWrite1h = r.CacheWrite5m
		}
	}
}

// Lookup finds the price for a rollup row's (provider, model). First hit wins:
//
//  1. override {provider, model}
//  2. override {"", model}
//  3. catalog[lower(model)]                                  bare key: "claude-opus-5-5", or an
//     already-prefixed id like "xai/grok-4.7"
//  4. catalog[p + "/" + lower(model)] for p in providerPrefixes[provider]
//     "xai/grok-4.7", "vertex_ai/gemini-3-pro"
//
// No fuzzy matching (no date stripping, no "-thinking" stripping): a silently
// wrong price is worse than an "unpriced" line telling Zac to add a same-as
// override. Antigravity ("claude-opus-4-6-thinking") and Devin ("devin/...") ids
// land in Report.UnpricedModels until an override maps them.
func (t *Table) Lookup(provider, model string) (Price, bool) {
	if t == nil {
		return Price{}, false
	}
	p, m := norm(provider), norm(model)
	if m == "" {
		return Price{}, false
	}
	if o, ok := t.overrides[overrideKey{p, m}]; ok {
		return o, true
	}
	if o, ok := t.overrides[overrideKey{"", m}]; ok {
		return o, true
	}
	return t.catalogLookup(p, m)
}

// catalogLookup applies rules 3 and 4 to an already normalized provider and key.
func (t *Table) catalogLookup(provider, key string) (Price, bool) {
	try := func(k string) (Price, bool) {
		e, ok := t.catalog.Entries[k]
		if !ok {
			return Price{}, false
		}
		return Price{Matched: k, Source: t.catalog.Source, Std: e.Std, Long: e.Long}, true
	}
	if p, ok := try(key); ok {
		return p, true
	}
	for _, prefix := range providerPrefixes[provider] {
		if p, ok := try(prefix + "/" + key); ok {
			return p, true
		}
	}
	return Price{}, false
}

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
