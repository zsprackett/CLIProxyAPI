package usagehistory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory/prices"
	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b)) }

func builtinTable(t *testing.T) *prices.Table {
	t.Helper()
	table, errs := prices.NewTable(prices.Builtin(), nil)
	if len(errs) > 0 {
		t.Fatalf("NewTable: %v", errs)
	}
	return table
}

func TestCostOfOpus55FromTheSnapshot(t *testing.T) {
	price, ok := builtinTable(t).Lookup("claude", "claude-opus-5-5")
	if !ok {
		t.Fatal("claude-opus-5-5 is not in the embedded snapshot")
	}
	const m = 1_000_000
	tokens := Tokens{Uncached: m, CacheRead: m, CacheWrite5m: m, CacheWrite1h: m, Output: m, Reasoning: m, Unclassified: 5}
	cost, priced, unpriced := costOf(tokens, price, true, false)
	want := CostByType{Uncached: 4, CacheRead: 0.2, CacheWrite5m: 5, CacheWrite1h: 8, Output: 20, Reasoning: 20}
	got := []float64{cost.Uncached, cost.CacheRead, cost.CacheWrite5m, cost.CacheWrite1h, cost.Output, cost.Reasoning}
	exp := []float64{want.Uncached, want.CacheRead, want.CacheWrite5m, want.CacheWrite1h, want.Output, want.Reasoning}
	for i := range got {
		if !near(got[i], exp[i]) {
			t.Fatalf("cost = %+v, want %+v", cost, want)
		}
	}
	if !near(cost.Sum(), 57.2) || priced != 6*m || unpriced != 5 {
		t.Fatalf("sum %v priced %d unpriced %d", cost.Sum(), priced, unpriced)
	}
}

func TestCostOfLongContextTier(t *testing.T) {
	price, ok := builtinTable(t).Lookup("xai", "grok-4.7")
	if !ok || price.Long == nil {
		t.Fatal("xai/grok-4.7 has no long-context tier in the snapshot")
	}
	tokens := Tokens{Uncached: 1_000_000}
	std, _, _ := costOf(tokens, price, true, false)
	long, _, _ := costOf(tokens, price, true, true)
	if !near(std.Uncached, 2) || !near(long.Uncached, 4) {
		t.Fatalf("std %v long %v", std.Uncached, long.Uncached)
	}
}

func TestAccumulatorPricing(t *testing.T) {
	day := Date{2026, time.October, 22}
	p := newPlan(day, day, time.UTC, UnitDay)
	hour := p.starts[0]
	acc := newAccumulator(p, builtinTable(t))
	acc.add(scannedCell{hour: hour, provider: "claude", model: "claude-opus-5-5", group: "priced",
		counters: Counters{Requests: 2, LatencyMsSum: 300, Tokens: Tokens{Output: 1_000_000, Unclassified: 10}}})
	acc.add(scannedCell{hour: hour, provider: "claude", model: "mystery", group: "unpriced",
		counters: Counters{Requests: 1, Tokens: Tokens{Output: 50}}})
	acc.add(scannedCell{hour: hour, provider: "claude", model: "mystery", group: "empty",
		counters: Counters{Requests: 1, Failures: 1}})
	rep := acc.finish()

	byKey := map[string]Cell{}
	for _, g := range rep.Groups {
		byKey[g.Key] = g.Cell
	}
	priced := byKey["priced"]
	if priced.CostUSD == nil || !near(*priced.CostUSD, 20) || priced.UnpricedTokens != 10 || priced.LatencyMs != 150 {
		t.Fatalf("priced = %+v", priced)
	}
	unpriced := byKey["unpriced"]
	if unpriced.CostUSD != nil || unpriced.CostByType != nil || unpriced.UnpricedTokens != 50 {
		t.Fatalf("unpriced = %+v", unpriced)
	}
	if empty := byKey["empty"]; empty.CostUSD == nil || *empty.CostUSD != 0 {
		t.Fatalf("a cell with no tokens costs 0, got %+v", empty)
	}
	if rep.Totals.CostUSD == nil || !near(*rep.Totals.CostUSD, 20) || rep.Totals.UnpricedTokens != 60 {
		t.Fatalf("totals = %+v", rep.Totals)
	}
	if len(rep.UnpricedModels) != 1 || rep.UnpricedModels[0] != "claude/mystery" {
		t.Fatalf("unpriced models = %v", rep.UnpricedModels)
	}
	if rep.Groups[0].Key != "priced" {
		t.Fatalf("groups not ranked by cost: %v", rep.Groups)
	}
	if len(rep.Buckets) != 1 || len(rep.Buckets[0].Groups) != 3 || rep.Buckets[0].Groups[0].Key != "empty" {
		t.Fatalf("bucket groups not sorted by key: %+v", rep.Buckets)
	}
}

func TestAccumulatorFoldsToTopGroups(t *testing.T) {
	day := Date{2026, time.October, 22}
	p := newPlan(day, day.addDays(1), time.UTC, UnitDay)
	acc := newAccumulator(p, builtinTable(t))
	const n = maxGroups + 5
	for i := 0; i < n; i++ {
		// group-00 has the most tokens; all are unpriced, so tokens rank them.
		acc.add(scannedCell{hour: p.starts[i%2], provider: "x", model: "m", group: fmt.Sprintf("group-%02d", i),
			counters: Counters{Requests: 1, Tokens: Tokens{Uncached: int64(1000 - i)}}})
	}
	rep := acc.finish()
	if rep.FoldedGroups != 5 || len(rep.Groups) != maxGroups+1 {
		t.Fatalf("folded %d, groups %d", rep.FoldedGroups, len(rep.Groups))
	}
	last := rep.Groups[len(rep.Groups)-1]
	if last.Key != OtherGroup || last.Requests != 5 || last.Tokens.Uncached != 1000*5-(30+31+32+33+34) {
		t.Fatalf("other = %+v", last)
	}
	if rep.Groups[0].Key != "group-00" || rep.Groups[maxGroups-1].Key != "group-29" {
		t.Fatalf("kept = %s .. %s", rep.Groups[0].Key, rep.Groups[maxGroups-1].Key)
	}
	var other int64
	for _, b := range rep.Buckets {
		for _, g := range b.Groups {
			if g.Key == OtherGroup {
				other += g.Requests
			}
			if g.Key == "group-34" {
				t.Fatal("a folded group is still in a bucket")
			}
		}
	}
	if other != 5 {
		t.Fatalf("other across buckets = %d", other)
	}
	if rep.Totals.Requests != n {
		t.Fatalf("totals = %d", rep.Totals.Requests)
	}
}

func TestReportCellCap(t *testing.T) {
	h := newHarness(t, nil)
	h.record(claudeRecord("m", 1))
	saved := maxCells
	maxCells = 10
	defer func() { maxCells = saved }()
	_, err := h.s.Report(context.Background(), Query{Loc: time.UTC, Unit: UnitHour, Group: DimModel})
	var tooLarge ErrTooLarge
	if !errors.As(err, &tooLarge) || tooLarge.Limit != 10 || tooLarge.Cells != 30*24 {
		t.Fatalf("err = %v", err)
	}
}

func TestReportDefaultsAndKeys(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.APIKeys = []string{"sk-test-key-123456"} })
	rec := claudeRecord("claude-opus-5-5", 1)
	rec.APIKey = "sk-test-key-123456"
	h.record(rec)
	toronto := mustLoc(t, "America/Toronto")
	rep := h.report(Query{Loc: toronto, Unit: UnitDay, Group: DimKey})
	if rep.Query.From != "2026-09-23" || rep.Query.To != "2026-10-22" || rep.Query.TZ != "America/Toronto" || rep.Query.Clamped {
		t.Fatalf("query = %+v", rep.Query)
	}
	if len(rep.Buckets) != 30 || rep.Buckets[29].Start != "2026-10-22T00:00:00-04:00" {
		t.Fatalf("buckets %d, last %+v", len(rep.Buckets), rep.Buckets[len(rep.Buckets)-1])
	}
	hash := HashClientKey("sk-test-key-123456")
	if label := rep.Keys[hash]; label == "" || label == "sk-test-key-123456" {
		t.Fatalf("keys = %v", rep.Keys)
	}
	if len(rep.Groups) != 1 || rep.Groups[0].Key != hash {
		t.Fatalf("groups = %+v", rep.Groups)
	}
	if rep.RecordingSince == nil || !rep.RecordingSince.Equal(harnessStart.Truncate(time.Hour)) {
		t.Fatalf("recording_since = %v", rep.RecordingSince)
	}
	if len(rep.Options.Provider) != 1 || rep.Options.Provider[0] != "claude" || rep.Prices.Source != "builtin" || rep.Prices.Remote {
		t.Fatalf("options %+v prices %+v", rep.Options, rep.Prices)
	}
}

// genRecords builds a deterministic mix of providers, models, keys, agents,
// hours, tiers, failures and Generate=false probes.
func genRecords(seed int64, n int, now time.Time) []struct {
	ctx context.Context
	rec coreusage.Record
} {
	rng := rand.New(rand.NewSource(seed))
	providers := []struct{ provider, model string }{
		{"claude", "claude-opus-5-5"}, {"claude", "claude-unknown-x"}, {"codex", "gpt-5.5"},
		{"xai", "grok-4.7"}, {"gemini", "gemini-x"}, {"devin", "devin/agent"},
	}
	agents := []string{"", "claude-cli/2.1", "codex_cli_rs/0.1", "curl/8"}
	out := make([]struct {
		ctx context.Context
		rec coreusage.Record
	}, n)
	for i := range out {
		pm := providers[rng.Intn(len(providers))]
		d := coreusage.Detail{
			InputTokens:  rng.Int63n(5000),
			OutputTokens: rng.Int63n(3000),
		}
		if pm.provider == "devin" {
			d = coreusage.Detail{TotalTokens: rng.Int63n(9000)}
		}
		if rng.Intn(5) == 0 {
			d.CacheReadTokens = LongContextThreshold + rng.Int63n(1000)
			if pm.provider != "claude" {
				d.InputTokens += d.CacheReadTokens
			}
		}
		rec := coreusage.Record{
			Provider:    pm.provider,
			Model:       pm.model,
			AuthID:      fmt.Sprintf("auth-%d", rng.Intn(3)),
			APIKey:      fmt.Sprintf("sk-%d", rng.Intn(4)),
			RequestedAt: now.Add(-time.Duration(rng.Intn(72*60)) * time.Minute),
			Latency:     time.Duration(rng.Intn(5000)) * time.Millisecond,
			Failed:      rng.Intn(7) == 0,
			Detail:      d,
		}
		if rng.Intn(10) == 0 {
			rec.Generate = coreusage.GenerateFlag(false)
		}
		out[i].ctx = internallogging.WithClientRequestMetadata(context.Background(),
			internallogging.ClientRequestMetadata{UserAgent: agents[rng.Intn(len(agents))]})
		out[i].rec = rec
	}
	return out
}

func groupValue(k Key, g Dimension) string {
	switch g {
	case DimModel:
		return k.Model
	case DimProvider:
		return k.Provider
	case DimKey:
		return k.KeyHash
	case DimAuth:
		return k.AuthID
	case DimAgent:
		return k.Agent
	}
	return "all"
}

// TestReportBeforeAndAfterFlushMatchesAReferenceFold is the DECISIONS property:
// a report with records still pending equals the one after a flush byte for
// byte, and both equal a fold over the raw records that never touches SQLite.
func TestReportBeforeAndAfterFlushMatchesAReferenceFold(t *testing.T) {
	for seed := int64(1); seed <= 5; seed++ {
		for _, g := range []Dimension{DimModel, DimKey, DimAgent, DimNone} {
			t.Run(fmt.Sprintf("seed%d/%s", seed, g), func(t *testing.T) {
				h := newHarness(t, nil)
				recs := genRecords(seed, 400, h.now())
				q := Query{Loc: mustLoc(t, "America/Toronto"), Unit: UnitHour, Group: g,
					From: Date{2026, time.October, 19}, To: Date{2026, time.October, 22}}

				half := len(recs) / 2
				for _, r := range recs[:half] {
					h.s.HandleUsage(r.ctx, r.rec)
				}
				h.tickNow()
				for _, r := range recs[half:] {
					h.s.HandleUsage(r.ctx, r.rec)
				}
				before := h.report(q)
				h.tickNow()
				after := h.report(q)
				jb, _ := json.Marshal(before)
				ja, _ := json.Marshal(after)
				if !bytes.Equal(jb, ja) {
					t.Fatalf("report changed across a flush:\n%s\n%s", jb, ja)
				}

				// Reference: rollup in memory, aggregate like the SQL GROUP BY, fold.
				p := newPlan(q.From, q.To, q.Loc, q.Unit)
				type cellKey struct {
					hour            int64
					provider, model string
					long            bool
					group           string
				}
				sums := map[cellKey]Counters{}
				for _, r := range recs {
					k, c, ok := rollupOf(r.ctx, r.rec, h.now())
					if !ok {
						continue
					}
					ck := cellKey{k.Hour, k.Provider, k.Model, k.LongCtx, groupValue(k, g)}
					sums[ck] = sums[ck].Add(c)
				}
				acc := newAccumulator(p, h.s.table.Load())
				for ck, c := range sums {
					if lo, hi := p.bounds(); ck.hour < lo || ck.hour >= hi {
						continue
					}
					acc.add(scannedCell{hour: ck.hour, provider: ck.provider, model: ck.model, longCtx: ck.long, group: ck.group, counters: c})
				}
				ref := acc.finish()
				if before.Totals.Requests == 0 {
					t.Fatal("generator produced no traffic in range")
				}
				assertSameCells(t, "totals", before.Totals, ref.Totals)
				if len(before.Groups) != len(ref.Groups) || before.FoldedGroups != ref.FoldedGroups {
					t.Fatalf("groups %d/%d folded %d/%d", len(before.Groups), len(ref.Groups), before.FoldedGroups, ref.FoldedGroups)
				}
				for i := range ref.Groups {
					if before.Groups[i].Key != ref.Groups[i].Key {
						t.Fatalf("group %d = %q, want %q", i, before.Groups[i].Key, ref.Groups[i].Key)
					}
					assertSameCells(t, "group "+ref.Groups[i].Key, before.Groups[i].Cell, ref.Groups[i].Cell)
				}
				if len(before.Buckets) != len(ref.Buckets) {
					t.Fatalf("buckets %d, want %d", len(before.Buckets), len(ref.Buckets))
				}
				for i := range ref.Buckets {
					rb, gb := ref.Buckets[i], before.Buckets[i]
					if rb.Start != gb.Start || len(rb.Groups) != len(gb.Groups) {
						t.Fatalf("bucket %d = %s/%d, want %s/%d", i, gb.Start, len(gb.Groups), rb.Start, len(rb.Groups))
					}
					for j := range rb.Groups {
						if rb.Groups[j].Key != gb.Groups[j].Key {
							t.Fatalf("bucket %d group %d = %q, want %q", i, j, gb.Groups[j].Key, rb.Groups[j].Key)
						}
						assertSameCells(t, fmt.Sprintf("bucket %d %s", i, rb.Groups[j].Key), gb.Groups[j].Cell, rb.Groups[j].Cell)
					}
				}
				if fmt.Sprint(before.UnpricedModels) != fmt.Sprint(ref.UnpricedModels) {
					t.Fatalf("unpriced %v, want %v", before.UnpricedModels, ref.UnpricedModels)
				}
			})
		}
	}
}

// assertSameCells compares integers exactly and costs within float rounding:
// the reference adds cells in map order, SQLite in key order.
func assertSameCells(t *testing.T, what string, got, want Cell) {
	t.Helper()
	if got.Tokens != want.Tokens || got.Requests != want.Requests || got.Failures != want.Failures ||
		got.UnpricedTokens != want.UnpricedTokens || !near(got.LatencyMs, want.LatencyMs) {
		t.Fatalf("%s = %+v, want %+v", what, got, want)
	}
	if (got.CostUSD == nil) != (want.CostUSD == nil) || (got.CostUSD != nil && !near(*got.CostUSD, *want.CostUSD)) {
		t.Fatalf("%s cost = %v, want %v", what, got.CostUSD, want.CostUSD)
	}
}
