package usagehistory

import (
	"context"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory/prices"
)

// Report answers GET /observability/usage/history. Never returns an error: a
// disabled or broken store is {enabled:false, reason}, HTTP 200, like the siblings.
//
// pseudocode:
//
//	cur := s.sync(ctx)                       // refresh config + flush: the report is current to now
//	if cur.phase != phaseOpen { return Report{Enabled:false, Reason: reasonOf(cur), empty slices} }
//	now := s.now()
//	q = fill defaults: To = today in q.Loc; From = To - 29 days
//	From, clamped := clampFrom(q.From, now, q.Loc, cur.retention)
//	if span > maxSpanDays(q.Unit) { From = To - maxSpan + 1; clamped = true }   // only reachable via defaults
//	p := newPlan(From, To, q.Loc, q.Unit); lo, hi := p.bounds()
//	table := s.table.Load()
//	ok := s.withReader(func(st, cur) {
//	    acc := newAccumulator(p, table)
//	    err = st.scanCells(ctx, lo, hi, q.Filter, q.Group, acc.add)
//	    opts, err = st.distinct(ctx, lo, hi)
//	    since, has, err = st.oldestHour(ctx)
//	})
//	any err -> log, Report{Enabled:false, Reason: unavailable}
//	rep := acc.finish()                      // buckets, groups (sorted), totals, unpriced models
//	rep.Keys = maskedKeys(cfg, hashes in opts.Key)
//	rep.Prices = catalogInfo(table, s.sched, s.lastErr)
func (s *Service) Report(ctx context.Context, q Query) Report { panic("not implemented") }

// accumulator folds scanned cells into the report. Pure given (plan, table).
//
// add(c):
//
//	i := plan.index(c.hour); if i < 0 { return }       // cannot happen with [lo,hi) but cheap to guard
//	price, priced := memo.lookup(c.provider, c.model)  // per-report memo over table.Lookup
//	cost := costOf(c.counters.Tokens, price, priced, c.longCtx)
//	cell := cellPart{counters: c.counters, cost: cost}
//	buckets[i][c.group] += cell; groups[c.group] += cell; totals += cell
//	if !priced && c.counters.Tokens.Total() > 0 { unpriced.add(c.provider + "/" + c.model) }
//
// finish():
//
//	every cellPart -> Cell:
//	  LatencyMs = latencySum / requests (0 when 0)
//	  CostUSD/CostByType = nil iff pricedTokens == 0 && Tokens.Total() > 0, else &sum
//	  UnpricedTokens = sum of unpriced parts
//	buckets[i].Groups sorted by key; Report.Groups sorted cost desc, tokens desc, key asc
type accumulator struct {
	plan     plan
	table    *prices.Table
	memo     map[[2]string]priceHit
	buckets  []map[string]*cellPart
	groups   map[string]*cellPart
	totals   cellPart
	unpriced map[string]struct{}
}

type priceHit struct {
	price prices.Price
	ok    bool
}

// cellPart is a Cell under construction (sums only; ratios computed in finish).
type cellPart struct {
	counters       Counters
	cost           CostByType
	pricedTokens   int64
	unpricedTokens int64
}

func newAccumulator(p plan, t *prices.Table) *accumulator { panic("not implemented") }
func (a *accumulator) add(c scannedCell)                  { panic("not implemented") }
func (a *accumulator) finish() Report                     { panic("not implemented") }

// costOf prices one cell's tokens. Pure.
//
//	if !priced: return CostByType{}, pricedTokens 0, unpricedTokens = t.Total()
//	r := price.RatesFor(longCtx)
//	cost = {Uncached: t.Uncached*r.Input, CacheRead: t.CacheRead*r.CacheRead,
//	        CacheWrite5m: t.CacheWrite5m*r.CacheWrite5m, CacheWrite1h: t.CacheWrite1h*r.CacheWrite1h,
//	        Output: t.Output*r.Output, Reasoning: t.Reasoning*r.Reasoning}
//	pricedTokens = t.Total() - t.Unclassified; unpricedTokens = t.Unclassified
func costOf(t Tokens, price prices.Price, priced, longCtx bool) (cost CostByType, pricedTokens, unpricedTokens int64) {
	panic("not implemented")
}

// maskedKeys labels key hashes present with util.HideAPIKey of the configured key
// (same rule as requestlog.maskedKeys; raw keys never leave the server).
func maskedKeys(cfg *config.Config, present []string) map[string]string { panic("not implemented") }

// ---------------------------------------------------------------------------
// GET /observability/usage/prices
// ---------------------------------------------------------------------------

// Per-million rates for display (USD per 1M tokens, the unit people compare).
type RatesPerMTok struct {
	Input        float64  `json:"input"`
	Output       float64  `json:"output"`
	Reasoning    float64  `json:"reasoning"`
	CacheRead    float64  `json:"cache_read"`
	CacheWrite5m float64  `json:"cache_write_5m"`
	CacheWrite1h float64  `json:"cache_write_1h"`
	Derived      []string `json:"derived,omitempty"`
}

// ModelPrice is the effective price of one (provider, model) seen in history or
// named by an override.
type ModelPrice struct {
	Provider    string        `json:"provider"`
	Model       string        `json:"model"`
	Priced      bool          `json:"priced"`
	Matched     string        `json:"matched,omitempty"` // catalog key or override label
	Source      prices.Source `json:"source,omitempty"`  // builtin | remote | override
	PerMTok     *RatesPerMTok `json:"per_mtok,omitempty"`
	LongContext *RatesPerMTok `json:"long_context,omitempty"` // prompts > 200k tokens
}

// PriceReport is the JSON body of GET /observability/usage/prices.
type PriceReport struct {
	Enabled bool         `json:"enabled"`
	Reason  Reason       `json:"reason,omitempty"`
	Catalog CatalogInfo  `json:"catalog"`
	Models  []ModelPrice `json:"models"` // sorted provider, model
}

// Prices lists the effective price of every (provider, model) in the retained
// history plus every override. Gated like Report.
func (s *Service) Prices(ctx context.Context) PriceReport { panic("not implemented") }

// ---------------------------------------------------------------------------
// Internal read for quotaforecast attribution.
// ---------------------------------------------------------------------------

// Hourly returns raw UTC hourly rows (tiers merged) since q.Since. Syncs first,
// so the current hour includes every record drained so far.
func (s *Service) Hourly(ctx context.Context, q HourlyQuery) HourlyResult { panic("not implemented") }

// hourStart truncates to the UTC hour (unix seconds).
func hourStart(t time.Time) int64 { return t.UTC().Truncate(time.Hour).Unix() }
