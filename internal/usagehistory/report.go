package usagehistory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory/prices"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	log "github.com/sirupsen/logrus"
)

// Response bounds. A report keeps the top maxGroups groups by cost, then tokens,
// and folds the rest into OtherGroup. maxCells caps buckets x groups after the
// fold; it is a var so a test can reach it, because the span limits keep real
// queries under it.
const maxGroups = 30

var maxCells = 50_000

// ErrTooLarge is the error Report returns when a report would exceed maxCells.
// The handler maps it to HTTP 400.
type ErrTooLarge struct{ Cells, Limit int }

func (e ErrTooLarge) Error() string {
	return fmt.Sprintf("report has %d bucket x group cells, more than the limit of %d; narrow the range or use a larger bucket", e.Cells, e.Limit)
}

// Report answers GET /observability/usage/history. A disabled or broken store is
// {enabled:false, reason} with a nil error, like the sibling services. The only
// error is ErrTooLarge.
func (s *Service) Report(ctx context.Context, q Query) (Report, error) {
	cur := s.sync(ctx)
	if cur.phase != phaseOpen {
		return disabledReport(reasonOf(cur), cur), nil
	}
	if q.Loc == nil {
		q.Loc = time.UTC
	}
	if q.Unit == "" {
		q.Unit = UnitDay
	}
	if q.Group == "" {
		q.Group = DimModel
	}
	now := s.now()
	from, to, clamped := effectiveRange(q, now, cur.retention)
	p := newPlan(from, to, q.Loc, q.Unit)
	lo, hi := p.bounds()
	table := s.table.Load()

	acc := newAccumulator(p, table)
	var opts Options
	var since int64
	var hasSince bool
	var err error
	ok := s.withReader(func(st *store) {
		if err = st.scanCells(ctx, lo, hi, q.Filter, q.Group, acc.add); err != nil {
			return
		}
		if opts, err = st.distinct(ctx, lo, hi); err != nil {
			return
		}
		since, hasSince, err = st.oldestHour(ctx)
	})
	if !ok || err != nil {
		if err != nil {
			log.WithError(err).Warn("usage history: report read failed")
		}
		return disabledReport(ReasonUnavailable, cur), nil
	}
	rep := acc.finish()
	if cells := len(rep.Buckets) * len(rep.Groups); cells > maxCells {
		return Report{}, ErrTooLarge{Cells: cells, Limit: maxCells}
	}
	rep.Enabled = true
	rep.Query = EffectiveQuery{
		From: from.String(), To: to.String(), TZ: q.Loc.String(),
		Bucket: q.Unit, GroupBy: q.Group, Clamped: clamped,
	}
	rep.AsOf = cur.asOf
	if hasSince {
		t := time.Unix(since, 0).UTC()
		rep.RecordingSince = &t
	}
	rep.RetentionDays = cur.retention
	rep.Options = opts
	rep.Keys = maskedKeys(s.config(), opts.Key)
	rep.Prices = cur.catalog
	return rep, nil
}

// effectiveRange fills default dates (the 30 days ending today in q.Loc) and
// applies the span and retention clamps. Explicit spans were checked by ParseQuery.
func effectiveRange(q Query, now time.Time, retention int) (from, to Date, clamped bool) {
	from, to = q.From, q.To
	if to.IsZero() {
		to = dateOf(now.In(q.Loc))
		if !from.IsZero() && to.before(from) {
			to = from
		}
	}
	if from.IsZero() {
		from = to.addDays(-29)
	}
	if limit := maxSpanDays(q.Unit); spanDays(from, to) > limit {
		from, clamped = to.addDays(-(limit - 1)), true
	}
	var moved bool
	from, moved = clampFrom(from, now, q.Loc, retention)
	return from, to, clamped || moved
}

func disabledReport(reason Reason, cur status) Report {
	return Report{
		Enabled:        false,
		Reason:         reason,
		RetentionDays:  cur.retention,
		Buckets:        []Bucket{},
		Groups:         []GroupCell{},
		UnpricedModels: []string{},
		Keys:           map[string]string{},
		Options:        emptyOptions(),
		Prices:         cur.catalog,
	}
}

func emptyOptions() Options {
	return Options{Provider: []string{}, Model: []string{}, Key: []string{}, Auth: []string{}, Agent: []string{}}
}

// accumulator folds scanned cells into the report. Pure given (plan, table).
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

func (c *cellPart) add(o cellPart) {
	c.counters = c.counters.Add(o.counters)
	c.cost = c.cost.Add(o.cost)
	c.pricedTokens += o.pricedTokens
	c.unpricedTokens += o.unpricedTokens
}

func (c cellPart) cell() Cell {
	out := Cell{
		Tokens:         c.counters.Tokens,
		Requests:       c.counters.Requests,
		Failures:       c.counters.Failures,
		UnpricedTokens: c.unpricedTokens,
	}
	if c.counters.Requests > 0 {
		out.LatencyMs = float64(c.counters.LatencyMsSum) / float64(c.counters.Requests)
	}
	if c.pricedTokens > 0 || c.counters.Tokens.Total() == 0 {
		cost, byType := c.cost.Sum(), c.cost
		out.CostUSD, out.CostByType = &cost, &byType
	}
	return out
}

func newAccumulator(p plan, t *prices.Table) *accumulator {
	a := &accumulator{
		plan:     p,
		table:    t,
		memo:     make(map[[2]string]priceHit),
		buckets:  make([]map[string]*cellPart, p.len()),
		groups:   make(map[string]*cellPart),
		unpriced: make(map[string]struct{}),
	}
	for i := range a.buckets {
		a.buckets[i] = make(map[string]*cellPart)
	}
	return a
}

func (a *accumulator) add(c scannedCell) {
	i := a.plan.index(c.hour)
	if i < 0 {
		return
	}
	memoKey := [2]string{c.provider, c.model}
	hit, seen := a.memo[memoKey]
	if !seen {
		hit.price, hit.ok = a.table.Lookup(c.provider, c.model)
		a.memo[memoKey] = hit
	}
	cost, priced, unpriced := costOf(c.counters.Tokens, hit.price, hit.ok, c.longCtx)
	part := cellPart{counters: c.counters, cost: cost, pricedTokens: priced, unpricedTokens: unpriced}
	addTo(a.buckets[i], c.group, part)
	addTo(a.groups, c.group, part)
	a.totals.add(part)
	if !hit.ok && c.counters.Tokens.Total() > 0 {
		a.unpriced[c.provider+"/"+c.model] = struct{}{}
	}
}

func addTo(m map[string]*cellPart, key string, part cellPart) {
	cur := m[key]
	if cur == nil {
		cur = &cellPart{}
		m[key] = cur
	}
	cur.add(part)
}

// finish turns the sums into the report body: every bucket of the plan, the
// groups ranked and folded to the top maxGroups, totals and unpriced models.
func (a *accumulator) finish() Report {
	ranked := make([]string, 0, len(a.groups))
	for k := range a.groups {
		ranked = append(ranked, k)
	}
	sort.Slice(ranked, func(i, j int) bool { return rankBefore(ranked[i], a.groups[ranked[i]], ranked[j], a.groups[ranked[j]]) })
	kept := ranked
	var folded []string
	if len(ranked) > maxGroups {
		kept, folded = ranked[:maxGroups], ranked[maxGroups:]
	}
	label := make(map[string]string, len(folded))
	for _, k := range folded {
		label[k] = OtherGroup
	}
	labelOf := func(k string) string {
		if l, ok := label[k]; ok {
			return l
		}
		return k
	}

	rep := Report{
		Buckets:        make([]Bucket, a.plan.len()),
		Groups:         make([]GroupCell, 0, len(kept)+1),
		FoldedGroups:   len(folded),
		Totals:         a.totals.cell(),
		UnpricedModels: sortedKeys(a.unpriced),
	}
	for _, k := range kept {
		rep.Groups = append(rep.Groups, GroupCell{Key: k, Cell: a.groups[k].cell()})
	}
	if len(folded) > 0 {
		var other cellPart
		for _, k := range folded {
			other.add(*a.groups[k])
		}
		rep.Groups = append(rep.Groups, GroupCell{Key: OtherGroup, Cell: other.cell()})
	}
	for i, parts := range a.buckets {
		start, end := a.plan.bucketTimes(i)
		merged := make(map[string]*cellPart, len(parts))
		for k, part := range parts {
			addTo(merged, labelOf(k), *part)
		}
		keys := make([]string, 0, len(merged))
		for k := range merged {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		groups := make([]GroupCell, 0, len(keys))
		for _, k := range keys {
			groups = append(groups, GroupCell{Key: k, Cell: merged[k].cell()})
		}
		rep.Buckets[i] = Bucket{Start: start, End: end, Groups: groups}
	}
	return rep
}

// rankBefore orders groups by cost desc, tokens desc, key asc.
func rankBefore(ka string, a *cellPart, kb string, b *cellPart) bool {
	if ca, cb := a.cost.Sum(), b.cost.Sum(); ca != cb {
		return ca > cb
	}
	if ta, tb := a.counters.Tokens.Total(), b.counters.Tokens.Total(); ta != tb {
		return ta > tb
	}
	return ka < kb
}

// costOf prices one cell's tokens. Pure. Unclassified tokens are never priced.
func costOf(t Tokens, price prices.Price, priced, longCtx bool) (cost CostByType, pricedTokens, unpricedTokens int64) {
	if !priced {
		return CostByType{}, 0, t.Total()
	}
	r := price.RatesFor(longCtx)
	cost = CostByType{
		Uncached:     float64(t.Uncached) * r.Input,
		CacheRead:    float64(t.CacheRead) * r.CacheRead,
		CacheWrite5m: float64(t.CacheWrite5m) * r.CacheWrite5m,
		CacheWrite1h: float64(t.CacheWrite1h) * r.CacheWrite1h,
		Output:       float64(t.Output) * r.Output,
		Reasoning:    float64(t.Reasoning) * r.Reasoning,
	}
	return cost, t.Total() - t.Unclassified, t.Unclassified
}

// maskedKeys labels the key hashes present with util.HideAPIKey of the configured
// key (the requestlog and quotaforecast rule; raw keys never leave the server).
func maskedKeys(cfg *config.Config, present []string) map[string]string {
	out := make(map[string]string, len(present))
	if cfg == nil {
		return out
	}
	want := make(map[string]bool, len(present))
	for _, h := range present {
		want[h] = true
	}
	for _, key := range cfg.APIKeys {
		if hash := HashClientKey(key); want[hash] {
			out[hash] = util.HideAPIKey(strings.TrimSpace(key))
		}
	}
	return out
}

// RatesPerMTok are rates in USD per 1M tokens, the unit people compare.
type RatesPerMTok struct {
	Input        float64  `json:"input"`
	Output       float64  `json:"output"`
	Reasoning    float64  `json:"reasoning"`
	CacheRead    float64  `json:"cache_read"`
	CacheWrite5m float64  `json:"cache_write_5m"`
	CacheWrite1h float64  `json:"cache_write_1h"`
	Derived      []string `json:"derived,omitempty"`
}

func perMTok(r prices.Rates) *RatesPerMTok {
	const m = 1e6
	return &RatesPerMTok{
		Input: r.Input * m, Output: r.Output * m, Reasoning: r.Reasoning * m,
		CacheRead: r.CacheRead * m, CacheWrite5m: r.CacheWrite5m * m, CacheWrite1h: r.CacheWrite1h * m,
		Derived: r.Derived,
	}
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
func (s *Service) Prices(ctx context.Context) PriceReport {
	cur := s.sync(ctx)
	rep := PriceReport{Reason: reasonOf(cur), Catalog: cur.catalog, Models: []ModelPrice{}}
	if cur.phase != phaseOpen {
		return rep
	}
	var keys [][2]string
	var err error
	since := hourStart(s.now()) - int64(cur.retention)*86400
	ok := s.withReader(func(st *store) { keys, err = st.priceKeys(ctx, since) })
	if !ok || err != nil {
		if err != nil {
			log.WithError(err).Warn("usage history: price keys read failed")
		}
		rep.Reason = ReasonUnavailable
		return rep
	}
	seen := make(map[[2]string]bool, len(keys)+len(cur.overrides))
	for _, k := range keys {
		seen[k] = true
	}
	for _, o := range cur.overrides {
		k := [2]string{strings.ToLower(strings.TrimSpace(o.Provider)), strings.TrimSpace(o.Model)}
		if k[1] != "" && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	table := s.table.Load()
	for _, k := range keys {
		mp := ModelPrice{Provider: k[0], Model: k[1]}
		if p, found := table.Lookup(k[0], k[1]); found {
			mp.Priced, mp.Matched, mp.Source = true, p.Matched, p.Source
			mp.PerMTok = perMTok(p.Std)
			if p.Long != nil {
				mp.LongContext = perMTok(*p.Long)
			}
		}
		rep.Models = append(rep.Models, mp)
	}
	rep.Enabled = true
	return rep
}

// Hourly returns raw UTC hourly rows (tiers merged) since q.Since. Syncs first,
// so the current hour includes every record drained so far.
func (s *Service) Hourly(ctx context.Context, q HourlyQuery) HourlyResult {
	cur := s.sync(ctx)
	if cur.phase != phaseOpen {
		return HourlyResult{Reason: reasonOf(cur)}
	}
	var rows []HourlyRow
	var err error
	ok := s.withReader(func(st *store) { rows, err = st.scanHourly(ctx, hourStart(q.Since), q.Providers) })
	if !ok || err != nil {
		if err != nil {
			log.WithError(err).Warn("usage history: hourly read failed")
		}
		return HourlyResult{Reason: ReasonUnavailable}
	}
	return HourlyResult{Enabled: true, Rows: rows}
}
