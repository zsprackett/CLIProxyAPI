package quotaforecast

import (
	"math"
	"sort"
	"time"
)

const (
	// minCoverage is how much history a pool needs before its burn rate is trusted.
	minCoverage = int64(time.Hour / time.Second)
	// minHorizon and maxHorizon bound how far a pool is projected.
	minHorizon = 6 * time.Hour
	maxHorizon = 8 * 24 * time.Hour
	// chartSamples is the number of chart intervals between start and end.
	chartSamples = 288
	// exhaustedEpsilon treats a pool with less than this many accounts left as empty.
	exhaustedEpsilon = 0.001
)

// Drain models describe how the router spreads load across a pool.
const (
	drainModelSpread     = "spread"
	drainModelSequential = "sequential"
)

// drainModelForStrategy maps a routing strategy to the drain model the projection uses.
func drainModelForStrategy(strategy string) string {
	switch strategy {
	case "fill-first", "soonest-reset":
		return drainModelSequential
	}
	return drainModelSpread
}

// member is one credential's recorded history within a pool.
type member struct {
	AuthID    string
	WindowSec int64
	Points    []Point
}

func (m member) last() Point { return m.Points[len(m.Points)-1] }

// segmentBurn returns the used percentage burned between two consecutive points
// and the instant from which that burn accrued (it always ends at b.T). Segments
// that cross an unexplained reset report ok=false and are excluded.
func segmentBurn(a, b Point) (burn float64, from int64, ok bool) {
	jitter := int64(resetJitter / time.Second)
	if a.Reset > 0 && b.T >= a.Reset {
		// The window reset between the points; what is used now was used after the reset.
		return b.Used, a.Reset, true
	}
	if a.Reset > 0 && b.Reset > a.Reset+jitter {
		return 0, 0, false
	}
	if b.Used < a.Used-0.5 {
		return 0, 0, false
	}
	return math.Max(0, b.Used-a.Used), a.T, true
}

// accountBurn returns the used percentage points burned within [from, to), with
// each segment's burn spread uniformly over the time it accrued.
func accountBurn(points []Point, from, to int64) float64 {
	var total float64
	for i := 1; i < len(points); i++ {
		if points[i].T <= from {
			continue
		}
		if points[i-1].T >= to {
			break
		}
		burn, start, ok := segmentBurn(points[i-1], points[i])
		if !ok || burn <= 0 {
			continue
		}
		end := points[i].T
		if end <= start {
			if end > from && end <= to {
				total += burn
			}
			continue
		}
		overlap := min(end, to) - max(start, from)
		if overlap > 0 {
			total += burn * float64(overlap) / float64(end-start)
		}
	}
	return total
}

// effectiveLookback limits lookback to the history the pool actually has.
func effectiveLookback(members []member, now, lookback int64) int64 {
	earliest := now
	for _, m := range members {
		if len(m.Points) > 0 && m.Points[0].T < earliest {
			earliest = m.Points[0].T
		}
	}
	return min(lookback, now-earliest)
}

// poolRate returns the pooled burn rate in accounts per hour over lookback seconds
// and the history it was measured over.
func poolRate(members []member, now, lookback int64) (float64, int64) {
	window := effectiveLookback(members, now, lookback)
	if window <= 0 {
		return 0, 0
	}
	var burned float64
	for _, m := range members {
		burned += accountBurn(m.Points, now-window, now)
	}
	return burned / 100 / (float64(window) / 3600), window
}

// settle returns the used percentage at now and the next future reset (0 when
// unknown), rolling a reset that already passed forward by whole windows.
func settle(last Point, windowSec, now int64) (float64, int64) {
	used, next := last.Used, last.Reset
	if next > 0 && next <= now {
		used = 0
		if windowSec > 0 {
			next += ((now-next)/windowSec + 1) * windowSec
		} else {
			next = 0
		}
	}
	return used, next
}

// valueAt returns the remaining percentage of an account at t from its history,
// or false when no observation precedes t.
func valueAt(points []Point, t int64) (float64, bool) {
	idx := sort.Search(len(points), func(i int) bool { return points[i].T > t }) - 1
	if idx < 0 {
		return 0, false
	}
	p := points[idx]
	if p.Reset > 0 && t >= p.Reset {
		return 100, true
	}
	return 100 - p.Used, true
}

type simAccount struct {
	id     string
	rem    float64
	next   int64
	window int64
}

type simResult struct {
	totals     []float64
	exhaustsAt int64
	recoversAt int64
	shortPP    float64
	spare      float64
	spareKnown bool
	trough     float64
	troughAt   int64
	zero       [][2]int64
	resets     []ChartReset
}

// simulate projects the pool over times (times[0] is now) burning ratePPH used
// percentage points per hour. Each account refills to 100% at its reset.
func simulate(initial []simAccount, ratePPH float64, drain string, times []int64) simResult {
	accts := make([]simAccount, len(initial))
	copy(accts, initial)
	total := func() float64 {
		var sum float64
		for _, a := range accts {
			sum += a.rem
		}
		return sum / 100
	}
	res := simResult{totals: make([]float64, len(times))}
	res.totals[0] = total()
	res.trough, res.troughAt = res.totals[0], times[0]
	zeroStart := int64(0)
	for i := 1; i < len(times); i++ {
		t := times[i]
		// Burn the interval ending at t first; resets due at t refill afterwards.
		burn := ratePPH * float64(t-times[i-1]) / 3600
		if drain == drainModelSequential {
			burn = drainSequentially(accts, burn)
		} else {
			burn = drainSpread(accts, burn)
		}
		res.shortPP += burn
		drained := total()
		empty := drained <= exhaustedEpsilon && ratePPH > 0
		if empty && res.exhaustsAt == 0 {
			res.exhaustsAt = t
		}
		if empty && zeroStart == 0 {
			zeroStart = t
		}
		for j := range accts {
			a := &accts[j]
			if a.next <= 0 || a.next > t {
				continue
			}
			if !res.spareKnown {
				res.spare, res.spareKnown = total(), true
			}
			res.resets = append(res.resets, ChartReset{At: a.next, AuthID: a.id})
			if res.exhaustsAt > 0 && res.recoversAt == 0 {
				res.recoversAt = a.next
			}
			a.rem = 100
			if a.window > 0 {
				for a.next <= t {
					a.next += a.window
				}
			} else {
				a.next = 0
			}
		}
		current := total()
		res.totals[i] = current
		if drained < res.trough {
			res.trough, res.troughAt = drained, t
		}
		if zeroStart != 0 && current > exhaustedEpsilon {
			res.zero = append(res.zero, [2]int64{zeroStart, t})
			zeroStart = 0
		}
	}
	if zeroStart != 0 {
		res.zero = append(res.zero, [2]int64{zeroStart, times[len(times)-1]})
	}
	return res
}

// drainSpread burns evenly across accounts with quota left and returns what no
// account could serve.
func drainSpread(accts []simAccount, burn float64) float64 {
	for burn > 1e-9 {
		live := 0
		for _, a := range accts {
			if a.rem > 0 {
				live++
			}
		}
		if live == 0 {
			return burn
		}
		share := burn / float64(live)
		burn = 0
		for j := range accts {
			if accts[j].rem <= 0 {
				continue
			}
			take := math.Min(share, accts[j].rem)
			accts[j].rem -= take
			burn += share - take
		}
	}
	return 0
}

// drainSequentially burns accounts one at a time in soonest-reset order and returns
// what no account could serve.
func drainSequentially(accts []simAccount, burn float64) float64 {
	order := make([]int, len(accts))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := accts[order[i]], accts[order[j]]
		if (a.next > 0) != (b.next > 0) {
			return a.next > 0
		}
		return a.next < b.next
	})
	for _, idx := range order {
		if burn <= 1e-9 {
			return 0
		}
		take := math.Min(burn, accts[idx].rem)
		accts[idx].rem -= take
		burn -= take
	}
	return math.Max(0, burn)
}

// poolOptions controls how much detail computePool produces.
type poolOptions struct {
	now          int64
	lookback     int64
	history      int64
	drain        string
	detail       bool
	usage        []UsageBucket
	maskedKeys   map[string]string
	accountNames map[string]string
}

// computePool derives the forecast of one pooled quota window class.
func computePool(class, provider string, members []member, accountsTotal int, opts poolOptions) Pool {
	now := opts.now
	windowSec := int64(0)
	for _, m := range members {
		windowSec = max(windowSec, m.WindowSec)
	}
	pool := Pool{
		Class:            class,
		Provider:         provider,
		Window:           classWindow(class),
		WindowSeconds:    windowSec,
		ModelScoped:      class == "claude:7d_oi",
		AccountsTotal:    accountsTotal,
		AccountsWithData: len(members),
	}

	central, coverage := poolRate(members, now, opts.lookback)
	low, high := central, central
	for _, hours := range []int64{6, 12, 24} {
		rate, _ := poolRate(members, now, hours*3600)
		low, high = math.Min(low, rate), math.Max(high, rate)
	}
	pointCount := 0
	for _, m := range members {
		pointCount += len(m.Points)
	}
	pool.Burn = Burn{
		AccountsPerHour: round4(central),
		Low:             round4(low),
		High:            round4(high),
		CoverageSeconds: coverage,
		Sufficient:      coverage >= minCoverage && pointCount >= 2,
	}

	accountWindow := effectiveLookback(members, now, opts.lookback)
	sims := make([]simAccount, 0, len(members))
	var usedSum, expectedSum float64
	paceCount := 0
	var nextReset int64
	for _, m := range members {
		used, next := settle(m.last(), m.WindowSec, now)
		sims = append(sims, simAccount{id: m.AuthID, rem: 100 - used, next: next, window: m.WindowSec})
		if next > 0 && (nextReset == 0 || next < nextReset) {
			nextReset = next
		}
		account := Account{
			AuthID:           m.AuthID,
			Name:             opts.accountNames[m.AuthID],
			UsedPercent:      round2(used),
			RemainingPercent: round2(100 - used),
			ResetAt:          unixPtr(next),
			ObservedAt:       time.Unix(m.last().T, 0).UTC(),
		}
		if accountWindow > 0 {
			account.BurnPercentPerHour = round4(accountBurn(m.Points, now-accountWindow, now) / (float64(accountWindow) / 3600))
		}
		if next > 0 && m.WindowSec > 0 {
			elapsed := clamp01(1 - float64(next-now)/float64(m.WindowSec))
			delta := used - 100*elapsed
			projected := used + account.BurnPercentPerHour*float64(next-now)/3600
			account.ElapsedFraction = floatPtr(round4(elapsed))
			account.PaceDeltaPP = floatPtr(round2(delta))
			account.ProjectedUsedAtReset = floatPtr(round2(projected))
			usedSum += used
			expectedSum += 100 * elapsed
			paceCount++
		}
		pool.RemainingAccounts += (100 - used) / 100
		if opts.detail {
			pool.Accounts = append(pool.Accounts, account)
		}
	}
	pool.RemainingAccounts = round4(pool.RemainingAccounts)
	pool.NextResetAt = unixPtr(nextReset)
	if paceCount > 0 {
		used, expected := usedSum/float64(paceCount), expectedSum/float64(paceCount)
		pool.Pace = &Pace{UsedPercent: round2(used), ExpectedPercent: round2(expected), DeltaPP: round2(used - expected)}
	}

	horizon := clampDuration(time.Duration(windowSec)*time.Second, minHorizon, maxHorizon)
	end := now + int64(horizon/time.Second)
	start := now - opts.history
	earliest := now
	for _, m := range members {
		earliest = min(earliest, m.Points[0].T)
	}
	start = min(max(start, earliest), now-3600)
	step := max((end-start)/chartSamples, 60)

	projTimes := []int64{now}
	for t := now + step; t < end; t += step {
		projTimes = append(projTimes, t)
	}
	projTimes = append(projTimes, end)
	centralRun := simulate(sims, central*100, opts.drain, projTimes)
	optimistic := simulate(sims, low*100, opts.drain, projTimes)
	pessimistic := simulate(sims, high*100, opts.drain, projTimes)

	runway := Runway{
		Status:         "ok",
		ExhaustsAt:     unixPtr(centralRun.exhaustsAt),
		RecoversAt:     unixPtr(centralRun.recoversAt),
		ShortAccounts:  round4(centralRun.shortPP / 100),
		TroughAccounts: round4(centralRun.trough),
		TroughAt:       unixPtr(centralRun.troughAt),
		HorizonEnd:     time.Unix(end, 0).UTC(),
	}
	if centralRun.spareKnown {
		runway.SpareAtNextResetAccounts = floatPtr(round4(centralRun.spare))
	}
	if centralRun.exhaustsAt > 0 {
		recovers := centralRun.recoversAt
		if recovers == 0 {
			recovers = end
		}
		runway.ShortSeconds = recovers - centralRun.exhaustsAt
	}
	switch {
	case !pool.Burn.Sufficient:
		runway.Status = "insufficient_data"
	case centralRun.exhaustsAt > 0:
		runway.Status = "short"
	case pessimistic.exhaustsAt > 0:
		runway.Status = "tight"
	}
	pool.Runway = runway

	if !opts.detail {
		return pool
	}

	chart := &Chart{Start: start, Now: now, End: end, StepSeconds: step}
	var histTimes []int64
	for t := start; t < now; t += step {
		histTimes = append(histTimes, t)
	}
	histTimes = append(histTimes, now)
	chart.History.Times = histTimes
	chart.History.Total = make([]*float64, len(histTimes))
	totals := make([]float64, len(histTimes))
	seen := make([]bool, len(histTimes))
	for _, m := range members {
		values := make([]*float64, len(histTimes))
		for i, t := range histTimes {
			if v, ok := valueAt(m.Points, t); ok {
				values[i] = floatPtr(round2(v))
				totals[i] += v / 100
				seen[i] = true
			}
		}
		chart.History.ByAccount = append(chart.History.ByAccount, ChartAccount{AuthID: m.AuthID, Values: values})
	}
	for i := range histTimes {
		if seen[i] {
			chart.History.Total[i] = floatPtr(round4(totals[i]))
		}
	}
	chart.Projection = ChartProjection{
		Times:     projTimes,
		Central:   round4All(centralRun.totals),
		BestCase:  round4All(optimistic.totals),
		WorstCase: round4All(pessimistic.totals),
	}
	chart.Resets = centralRun.resets
	for _, z := range centralRun.zero {
		chart.Danger = append(chart.Danger, ChartDanger{From: z[0], To: z[1], Severity: "projected"})
	}
	for _, z := range pessimistic.zero {
		chart.Danger = append(chart.Danger, ChartDanger{From: z[0], To: z[1], Severity: "possible"})
	}
	pool.Chart = chart
	pool.HourlyBurn = hourlyBurn(members, now)
	pool.Attribution = attribute(members, opts.usage, opts.maskedKeys, now)
	return pool
}

// hourlyBurn returns [hour start, accounts burned, coverage] for every hour with
// recorded history, where coverage is the fraction of the pool observed in that hour.
func hourlyBurn(members []member, now int64) [][3]float64 {
	if len(members) == 0 {
		return nil
	}
	earliest := now
	for _, m := range members {
		earliest = min(earliest, m.Points[0].T)
	}
	var out [][3]float64
	for h := earliest - earliest%3600; h < now; h += 3600 {
		var burned float64
		covered := 0
		for _, m := range members {
			if m.Points[0].T > h+3600 || m.last().T < h {
				continue
			}
			covered++
			burned += accountBurn(m.Points, h, h+3600)
		}
		if covered == 0 {
			continue
		}
		out = append(out, [3]float64{float64(h), round4(burned / 100), round4(float64(covered) / float64(len(members)))})
	}
	return out
}

// classWindow returns the window part of a class such as "7d" for "claude:7d".
func classWindow(class string) string {
	for i := 0; i < len(class); i++ {
		if class[i] == ':' {
			return class[i+1:]
		}
	}
	return class
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

func clampDuration(d, lo, hi time.Duration) time.Duration {
	if d < lo {
		return lo
	}
	if d > hi {
		return hi
	}
	return d
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }
func round4(v float64) float64 { return math.Round(v*10000) / 10000 }

func round4All(values []float64) []float64 {
	out := make([]float64, len(values))
	for i, v := range values {
		out[i] = round4(v)
	}
	return out
}

func floatPtr(v float64) *float64 { return &v }

func unixPtr(sec int64) *time.Time {
	if sec <= 0 {
		return nil
	}
	t := time.Unix(sec, 0).UTC()
	return &t
}
