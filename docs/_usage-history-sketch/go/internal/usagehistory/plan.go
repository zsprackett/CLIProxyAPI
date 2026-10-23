package usagehistory

import "time"

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
// hourly rollups, which Zac fixed.
type plan struct {
	loc    *time.Location
	unit   Unit
	starts []int64 // unix seconds
}

// newPlan builds the buckets for [from, to] inclusive in loc.
//
// pseudocode (all boundaries via time.Date in loc, which is DST-correct):
//
//	switch unit
//	case day:   cur = Date(from.Y, from.M, from.D, 0,0,0,0, loc)
//	            end = Date(to.Y, to.M, to.D+1, 0,0,0,0, loc)
//	            step(cur) = Date(cur.Y, cur.M, cur.D+1, 0,0,0,0, loc)
//	            // a DST day is 23h or 25h long because the NEXT midnight is computed
//	            // in civil time, not by adding 24h. Zones whose DST jump happens at
//	            // midnight (e.g. America/Santiago, America/Havana): time.Date normalizes
//	            // the nonexistent 00:00 to 01:00 (Go picks the instant after the gap),
//	            // so the day still starts at its first existing instant.
//	case month: cur = Date(from.Y, from.M, 1, ...), end = Date(to.Y, to.M+1, 1, ...), step = month+1
//	case hour:  cur = local midnight of from; end = local midnight after to
//	            step(cur) = cur.Add(time.Hour)   // ABSOLUTE hours: a fall-back day has 25
//	            // buckets, the repeated 01:00 appears twice with different offsets in Start;
//	            // a spring-forward day has 23 and no 02:00 bucket.
//	            // In :30 zones local midnight is xx:30 UTC; hourly buckets then start at :30
//	            // UTC and each holds exactly one UTC rollup hour (the one starting 30 min earlier
//	            // falls in the previous bucket by the start rule) -- labels stay local.
//	for cur < end { starts = append(starts, cur.Unix()); cur = step(cur) }
//	starts = append(starts, end.Unix())
func newPlan(from, to Date, loc *time.Location, unit Unit) plan { panic("not implemented") }

// len is the number of buckets.
func (p plan) len() int { return len(p.starts) - 1 }

// bounds returns the UTC hour range to query: [lo, hi).
func (p plan) bounds() (lo, hi int64) { panic("not implemented") }

// index returns the bucket holding the UTC hour starting at hour, or -1.
// sort.Search over starts: largest i with starts[i] <= hour, valid if i < len().
func (p plan) index(hour int64) int { panic("not implemented") }

// bucketTimes returns Start/End of bucket i formatted RFC3339 in loc.
func (p plan) bucketTimes(i int) (start, end string) { panic("not implemented") }

// clampFrom moves from forward so the plan does not reach past the retention
// horizon (now - retentionDays, as a local date). Reports whether it moved.
func clampFrom(from Date, now time.Time, loc *time.Location, retentionDays int) (Date, bool) {
	panic("not implemented")
}
