package usagehistory

import (
	"sort"
	"time"
)

// plan maps UTC rollup hours onto local-time buckets.
//
// Invariant: starts is strictly ascending and has len(buckets)+1 entries; bucket i
// is the half-open UTC interval [starts[i], starts[i+1]). The store is queried for
// hour in [starts[0], starts[len-1]).
//
// Assignment rule: a UTC rollup hour belongs to the bucket that contains its START
// instant. In zones whose offset is a whole number of hours this is exact. In
// :30/:45 zones (India, Nepal, parts of Australia) a local midnight falls inside a
// UTC hour, and that hour's usage lands wholly in the bucket where it started:
// up to 30/45 minutes of usage shift across a day boundary. That is the cost of
// hourly rollups.
type plan struct {
	loc    *time.Location
	unit   Unit
	starts []int64 // unix seconds
}

// newPlan builds the buckets for [from, to] inclusive in loc. Every boundary is
// computed with time.Date in loc, so a DST day is 23h or 25h long: the next
// midnight is found in civil time, not by adding 24h. Where a DST jump skips
// midnight itself, time.Date normalizes to the first existing instant.
//
// Hour buckets step in absolute hours: a fall-back day has 25 buckets (01:00
// twice, with different offsets), a spring-forward day 23 (no 02:00).
func newPlan(from, to Date, loc *time.Location, unit Unit) plan {
	var cur, end time.Time
	var step func(time.Time) time.Time
	switch unit {
	case UnitMonth:
		cur = time.Date(from.Year, from.Month, 1, 0, 0, 0, 0, loc)
		end = time.Date(to.Year, to.Month+1, 1, 0, 0, 0, 0, loc)
		step = func(t time.Time) time.Time {
			y, m, _ := t.Date()
			return time.Date(y, m+1, 1, 0, 0, 0, 0, loc)
		}
	case UnitHour:
		cur = time.Date(from.Year, from.Month, from.Day, 0, 0, 0, 0, loc)
		end = time.Date(to.Year, to.Month, to.Day+1, 0, 0, 0, 0, loc)
		step = func(t time.Time) time.Time { return t.Add(time.Hour) }
	default:
		cur = time.Date(from.Year, from.Month, from.Day, 0, 0, 0, 0, loc)
		end = time.Date(to.Year, to.Month, to.Day+1, 0, 0, 0, 0, loc)
		step = func(t time.Time) time.Time {
			y, m, d := t.Date()
			return time.Date(y, m, d+1, 0, 0, 0, 0, loc)
		}
	}
	p := plan{loc: loc, unit: unit}
	for cur.Before(end) {
		p.starts = append(p.starts, cur.Unix())
		cur = step(cur)
	}
	p.starts = append(p.starts, end.Unix())
	return p
}

// len is the number of buckets.
func (p plan) len() int { return len(p.starts) - 1 }

// bounds returns the UTC range to query: [lo, hi).
func (p plan) bounds() (lo, hi int64) { return p.starts[0], p.starts[len(p.starts)-1] }

// index returns the bucket holding the UTC hour starting at hour, or -1.
func (p plan) index(hour int64) int {
	i := sort.Search(len(p.starts), func(i int) bool { return p.starts[i] > hour }) - 1
	if i < 0 || i >= p.len() {
		return -1
	}
	return i
}

// bucketTimes returns Start/End of bucket i formatted RFC3339 in loc.
func (p plan) bucketTimes(i int) (start, end string) {
	return time.Unix(p.starts[i], 0).In(p.loc).Format(time.RFC3339),
		time.Unix(p.starts[i+1], 0).In(p.loc).Format(time.RFC3339)
}

// clampFrom moves from forward so the plan does not reach past the retention
// horizon (now - retentionDays, as a local date). Reports whether it moved.
func clampFrom(from Date, now time.Time, loc *time.Location, retentionDays int) (Date, bool) {
	horizon := dateOf(now.Add(-time.Duration(retentionDays) * 24 * time.Hour).In(loc))
	if from.before(horizon) {
		return horizon, true
	}
	return from, false
}
