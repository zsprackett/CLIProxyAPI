package usagehistory

import (
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("LoadLocation(%q): %v", name, err)
	}
	return loc
}

func utcHour(t *testing.T, s string) int64 {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return ts.Unix()
}

func bucketStarts(p plan) []string {
	out := make([]string, p.len())
	for i := range out {
		out[i], _ = p.bucketTimes(i)
	}
	return out
}

func TestPlanDayBucketsAcrossTorontoDST(t *testing.T) {
	toronto := mustLoc(t, "America/Toronto")
	cases := []struct {
		name      string
		day       Date
		wantHours float64
		wantStart string
		wantEnd   string
	}{
		{"spring forward", Date{2026, time.March, 8}, 23, "2026-03-08T00:00:00-05:00", "2026-03-09T00:00:00-04:00"},
		{"fall back", Date{2026, time.November, 1}, 25, "2026-11-01T00:00:00-04:00", "2026-11-02T00:00:00-05:00"},
		{"plain", Date{2026, time.June, 1}, 24, "2026-06-01T00:00:00-04:00", "2026-06-02T00:00:00-04:00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPlan(c.day, c.day, toronto, UnitDay)
			if p.len() != 1 {
				t.Fatalf("len = %d, want 1", p.len())
			}
			lo, hi := p.bounds()
			if got := float64(hi-lo) / 3600; got != c.wantHours {
				t.Fatalf("day is %vh, want %vh", got, c.wantHours)
			}
			start, end := p.bucketTimes(0)
			if start != c.wantStart || end != c.wantEnd {
				t.Fatalf("bucket = %s..%s, want %s..%s", start, end, c.wantStart, c.wantEnd)
			}
		})
	}
}

func TestPlanHourBucketsOnFallBackDay(t *testing.T) {
	toronto := mustLoc(t, "America/Toronto")
	day := Date{2026, time.November, 1}
	p := newPlan(day, day, toronto, UnitHour)
	if p.len() != 25 {
		t.Fatalf("fall-back day has %d hour buckets, want 25", p.len())
	}
	starts := bucketStarts(p)
	if starts[1] != "2026-11-01T01:00:00-04:00" || starts[2] != "2026-11-01T01:00:00-05:00" {
		t.Fatalf("repeated 01:00 buckets = %s, %s", starts[1], starts[2])
	}
	// The UTC hour 06:00Z is 01:00 EST, the second 01:00 bucket.
	if got := p.index(utcHour(t, "2026-11-01T06:00:00Z")); got != 2 {
		t.Fatalf("index(06:00Z) = %d, want 2", got)
	}
}

func TestPlanHourBucketsOnSpringForwardDay(t *testing.T) {
	toronto := mustLoc(t, "America/Toronto")
	day := Date{2026, time.March, 8}
	p := newPlan(day, day, toronto, UnitHour)
	if p.len() != 23 {
		t.Fatalf("spring-forward day has %d hour buckets, want 23", p.len())
	}
	starts := bucketStarts(p)
	if starts[2] != "2026-03-08T03:00:00-04:00" {
		t.Fatalf("bucket after 01:00 = %s, want 03:00 (no 02:00)", starts[2])
	}
}

func TestPlanMonthBucketsAcrossDST(t *testing.T) {
	toronto := mustLoc(t, "America/Toronto")
	p := newPlan(Date{2026, time.February, 15}, Date{2026, time.April, 3}, toronto, UnitMonth)
	want := []string{"2026-02-01T00:00:00-05:00", "2026-03-01T00:00:00-05:00", "2026-04-01T00:00:00-04:00"}
	got := bucketStarts(p)
	if len(got) != len(want) {
		t.Fatalf("starts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("starts = %v, want %v", got, want)
		}
	}
	if _, end := p.bucketTimes(2); end != "2026-05-01T00:00:00-04:00" {
		t.Fatalf("last end = %s", end)
	}
	// March is 31 days minus the lost hour.
	if hours := (p.starts[2] - p.starts[1]) / 3600; hours != 31*24-1 {
		t.Fatalf("March has %d hours, want %d", hours, 31*24-1)
	}
}

func TestPlanHalfHourZoneAssignsByHourStart(t *testing.T) {
	kolkata := mustLoc(t, "Asia/Kolkata")
	p := newPlan(Date{2026, time.May, 9}, Date{2026, time.May, 10}, kolkata, UnitDay)
	lo, hi := p.bounds()
	if lo != utcHour(t, "2026-05-08T18:30:00Z") || hi != utcHour(t, "2026-05-10T18:30:00Z") {
		t.Fatalf("bounds = %d..%d", lo, hi)
	}
	cases := []struct {
		hour string
		want int
	}{
		{"2026-05-08T18:00:00Z", -1}, // starts 23:30 on May 8: before the range
		{"2026-05-09T18:00:00Z", 0},  // starts 23:30 on May 9
		{"2026-05-09T19:00:00Z", 1},  // starts 00:30 on May 10
		{"2026-05-10T18:00:00Z", 1},  // starts 23:30 on May 10
		{"2026-05-10T19:00:00Z", -1}, // after the range
	}
	for _, c := range cases {
		if got := p.index(utcHour(t, c.hour)); got != c.want {
			t.Errorf("index(%s) = %d, want %d", c.hour, got, c.want)
		}
	}
	if start, _ := p.bucketTimes(1); start != "2026-05-10T00:00:00+05:30" {
		t.Fatalf("start = %s", start)
	}
	hp := newPlan(Date{2026, time.May, 10}, Date{2026, time.May, 10}, kolkata, UnitHour)
	if hp.len() != 24 {
		t.Fatalf("hour buckets = %d", hp.len())
	}
	// Each half-hour-offset bucket holds exactly the UTC hour that starts inside it.
	if got := hp.index(utcHour(t, "2026-05-09T19:00:00Z")); got != 0 {
		t.Fatalf("index(19:00Z) = %d, want 0", got)
	}
}

func TestPlanUTC(t *testing.T) {
	p := newPlan(Date{2026, time.January, 30}, Date{2026, time.February, 2}, time.UTC, UnitDay)
	if p.len() != 4 {
		t.Fatalf("len = %d", p.len())
	}
	if start, end := p.bucketTimes(3); start != "2026-02-02T00:00:00Z" || end != "2026-02-03T00:00:00Z" {
		t.Fatalf("bucket 3 = %s..%s", start, end)
	}
	if got := p.index(utcHour(t, "2026-02-01T23:00:00Z")); got != 2 {
		t.Fatalf("index = %d", got)
	}
}

func TestClampFrom(t *testing.T) {
	toronto := mustLoc(t, "America/Toronto")
	now := time.Date(2026, time.October, 22, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		from      Date
		retention int
		want      Date
		moved     bool
	}{
		{Date{2026, time.October, 1}, 365, Date{2026, time.October, 1}, false},
		{Date{2024, time.January, 1}, 365, Date{2025, time.October, 22}, true},
		{Date{2026, time.October, 20}, 1, Date{2026, time.October, 21}, true},
		{Date{2026, time.October, 21}, 1, Date{2026, time.October, 21}, false},
	}
	for _, c := range cases {
		got, moved := clampFrom(c.from, now, toronto, c.retention)
		if got != c.want || moved != c.moved {
			t.Errorf("clampFrom(%s, %d) = %s %v, want %s %v", c.from, c.retention, got, moved, c.want, c.moved)
		}
	}
}
