package quotaforecast

import (
	"math"
	"testing"
)

const hour = int64(3600)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestSegmentBurnExcludesResetJumps(t *testing.T) {
	cases := []struct {
		name string
		a, b Point
		burn float64
		from int64
		ok   bool
	}{
		{"steady burn", Point{T: 0, Used: 10, Reset: 100 * hour}, Point{T: hour, Used: 14, Reset: 100 * hour}, 4, 0, true},
		{"crossed known reset", Point{T: 0, Used: 90, Reset: hour}, Point{T: 3 * hour, Used: 6, Reset: 169 * hour}, 6, hour, true},
		{"unexplained reset", Point{T: 0, Used: 90, Reset: 10 * hour}, Point{T: hour, Used: 5, Reset: 200 * hour}, 0, 0, false},
		{"drop without reset time", Point{T: 0, Used: 50}, Point{T: hour, Used: 2}, 0, 0, false},
		{"rounding wobble", Point{T: 0, Used: 50}, Point{T: hour, Used: 49.8}, 0, 0, true},
	}
	for _, tc := range cases {
		burn, from, ok := segmentBurn(tc.a, tc.b)
		if !near(burn, tc.burn) || from != tc.from || ok != tc.ok {
			t.Fatalf("%s: got (%v,%v,%v), want (%v,%v,%v)", tc.name, burn, from, ok, tc.burn, tc.from, tc.ok)
		}
	}
}

func TestAccountBurnSpreadsOverInterval(t *testing.T) {
	points := []Point{{T: 0, Used: 0, Reset: 1000 * hour}, {T: 4 * hour, Used: 8, Reset: 1000 * hour}}
	if got := accountBurn(points, hour, 2*hour); !near(got, 2) {
		t.Fatalf("burn in one of four hours = %v, want 2", got)
	}
	if got := accountBurn(points, 5*hour, 6*hour); got != 0 {
		t.Fatalf("burn outside the segment = %v", got)
	}
}

func TestPoolRateUsesCoverageLimitedLookback(t *testing.T) {
	now := 10 * hour
	members := []member{
		{AuthID: "a", Points: []Point{{T: 8 * hour, Used: 0, Reset: 100 * hour}, {T: 10 * hour, Used: 10, Reset: 100 * hour}}},
		{AuthID: "b", Points: []Point{{T: 8 * hour, Used: 0, Reset: 100 * hour}, {T: 10 * hour, Used: 30, Reset: 100 * hour}}},
	}
	rate, window := poolRate(members, now, 12*hour)
	if window != 2*hour || !near(rate, 0.2) {
		t.Fatalf("rate = %v over %v, want 0.2 accounts/h over 2h", rate, window)
	}
}

func TestSettleRollsPastResetsForward(t *testing.T) {
	used, next := settle(Point{T: 0, Used: 80, Reset: 10}, 100, 250)
	if used != 0 || next != 310 {
		t.Fatalf("settle = (%v,%v), want (0,310)", used, next)
	}
	used, next = settle(Point{T: 0, Used: 80, Reset: 500}, 100, 250)
	if used != 80 || next != 500 {
		t.Fatalf("future reset changed: (%v,%v)", used, next)
	}
}

func TestSimulateExhaustsAndRecovers(t *testing.T) {
	times := []int64{0}
	for t := hour; t <= 10*hour; t += hour {
		times = append(times, t)
	}
	accts := []simAccount{
		{id: "a", rem: 20, next: 6 * hour, window: 100 * hour},
		{id: "b", rem: 20, next: 0},
	}
	// 10pp/h drains 40pp in 4h; account a refills at 6h.
	res := simulate(accts, 10, drainModelSpread, times)
	if res.exhaustsAt != 4*hour || res.recoversAt != 6*hour {
		t.Fatalf("exhausts=%d recovers=%d, want 4h and 6h", res.exhaustsAt/hour, res.recoversAt/hour)
	}
	if !near(res.shortPP, 20) {
		t.Fatalf("unserved = %v pp, want 20 (2h at 10pp/h)", res.shortPP)
	}
	if !res.spareKnown || res.spare != 0 {
		t.Fatalf("spare before first reset = %v", res.spare)
	}
	if len(res.zero) != 1 || res.zero[0] != [2]int64{4 * hour, 6 * hour} {
		t.Fatalf("zero intervals = %v", res.zero)
	}
	if accts[0].rem != 20 {
		t.Fatal("simulate must not mutate its input")
	}
}

func TestDrainModels(t *testing.T) {
	spread := []simAccount{{id: "a", rem: 5}, {id: "b", rem: 50}}
	if left := drainSpread(spread, 20); left != 0 || spread[0].rem != 0 || spread[1].rem != 35 {
		t.Fatalf("spread spill: %+v left %v", spread, left)
	}
	seq := []simAccount{{id: "late", rem: 50, next: 20}, {id: "soon", rem: 50, next: 10}, {id: "unknown", rem: 50}}
	if left := drainSequentially(seq, 60); left != 0 || seq[1].rem != 0 || seq[0].rem != 40 || seq[2].rem != 50 {
		t.Fatalf("sequential order: %+v", seq)
	}
}

func TestComputePoolStatusesAndPace(t *testing.T) {
	now := 100 * hour
	week := 7 * 24 * hour
	// Two accounts burning 2pp/h each, both resetting in 3 days, with 50% left each.
	mk := func(id string) member {
		return member{AuthID: id, WindowSec: week, Points: []Point{
			{T: now - 12*hour, Used: 26, Reset: now + 72*hour},
			{T: now, Used: 50, Reset: now + 72*hour},
		}}
	}
	opts := poolOptions{now: now, lookback: 12 * hour, history: 24 * hour, drain: drainModelSpread, detail: true}
	pool := computePool("claude:7d", "claude", []member{mk("a"), mk("b")}, 2, opts)
	if !near(pool.Burn.AccountsPerHour, 0.04) || !pool.Burn.Sufficient {
		t.Fatalf("burn = %+v", pool.Burn)
	}
	if !near(pool.RemainingAccounts, 1) {
		t.Fatalf("remaining = %v", pool.RemainingAccounts)
	}
	// 1 account left at 0.04/h runs out in 25h, well before the 72h reset.
	if pool.Runway.Status != "short" || pool.Runway.ExhaustsAt == nil || pool.Runway.RecoversAt == nil {
		t.Fatalf("runway = %+v", pool.Runway)
	}
	if got := pool.Runway.ExhaustsAt.Unix() - now; got < 24*hour || got > 27*hour {
		t.Fatalf("exhausts in %dh, want about 25h", got/hour)
	}
	// 4 of 7 days elapsed → expected 57.14%; used 50% → about 7pp under pace.
	if pool.Pace == nil || !near(pool.Pace.DeltaPP, -7.14) {
		t.Fatalf("pace = %+v", pool.Pace)
	}
	if pool.Chart == nil || len(pool.Chart.History.ByAccount) != 2 || pool.Chart.Projection.Times[0] != now {
		t.Fatalf("chart = %+v", pool.Chart)
	}
	if len(pool.Chart.Danger) == 0 || pool.Chart.Danger[0].Severity != "projected" {
		t.Fatalf("danger = %+v", pool.Chart.Danger)
	}
	if a := pool.Accounts[0]; a.ProjectedUsedAtReset == nil || *a.ProjectedUsedAtReset <= 100 {
		t.Fatalf("account should be projected past 100%% at reset: %+v", a)
	}

	opts.detail = false
	idle := computePool("claude:7d", "claude", []member{{AuthID: "a", WindowSec: week, Points: []Point{{T: now - 10*60, Used: 5, Reset: now + hour}}}}, 1, opts)
	if idle.Runway.Status != "insufficient_data" || idle.Chart != nil || idle.Accounts != nil {
		t.Fatalf("idle pool = %+v", idle)
	}
}

func TestHourlyBurnCoverage(t *testing.T) {
	now := 3 * hour
	members := []member{
		{AuthID: "a", Points: []Point{{T: 0, Used: 0, Reset: 100 * hour}, {T: 2 * hour, Used: 20, Reset: 100 * hour}}},
		{AuthID: "b", Points: []Point{{T: 2*hour + 60, Used: 0, Reset: 100 * hour}}},
	}
	got := hourlyBurn(members, now)
	if len(got) != 3 {
		t.Fatalf("hours = %v", got)
	}
	if got[0][1] != 0.1 || got[0][2] != 0.5 {
		t.Fatalf("first hour = %v, want 0.1 accounts at 50%% coverage", got[0])
	}
	if got[2][2] != 1 {
		t.Fatalf("last hour should cover both accounts: %v", got[2])
	}
}
