package usagehistory

import (
	"errors"
	"net/url"

	// Windows release builds have no system zoneinfo; without this, every tz other
	// than UTC/Local fails time.LoadLocation there. ~450 KB.
	_ "time/tzdata"
)

// Span limits per unit. They bound response size (buckets x groups), not the store.
const (
	maxHourSpanDays  = 31   // 744 hourly buckets
	maxDaySpanDays   = 1100 // ~3 years of daily buckets
	maxMonthSpanDays = 3660 // 120 months
	maxFilterLen     = 200
)

var errNotImplemented = errors.New("not implemented")

// ParseQuery turns raw query parameters into a validated Query. It is the HTTP
// boundary: everything after it trusts the Query.
//
//	from, to  YYYY-MM-DD civil dates in tz, both inclusive. Either may be empty
//	          (defaults are filled by Report because they depend on "now").
//	tz        IANA name (America/Toronto). Empty = UTC. Unknown = 400.
//	bucket    hour | day | month. Empty = day.
//	group     model | provider | key | auth | agent | none. Empty = model.
//	provider, model, key, auth, agent   exact-match filters, each <= 200 bytes.
//
// Errors (all 400): unparseable date, to < from, unknown tz/bucket/group,
// span over the unit's limit (hour 31 d, day 1100 d, month 3660 d), repeated
// parameter, filter too long. ParseQuery never consults config or the clock.
func ParseQuery(values url.Values) (Query, error) {
	// pseudocode:
	//   one := func(name) (string, error) { vs := values[name]; len(vs) > 1 -> error "<name> given twice"; trim }
	//   loc := time.UTC; if tz != "" { loc, err = time.LoadLocation(tz); err -> "tz must be an IANA time zone" }
	//   unit := parseUnit(bucket) ; group := parseDimension(group)
	//   from, to := parseDate(...)   // time.Parse("2006-01-02", s) -> Date
	//   if both set: to < from -> error; spanDays(from,to) > maxSpan(unit) -> error naming the limit
	//   if only one set, the span check happens again in Report after defaults (clamped, not an error)
	//   filters: trim; len > maxFilterLen -> error
	return Query{}, errNotImplemented
}

func parseUnit(s string) (Unit, error)           { return "", errNotImplemented }
func parseDimension(s string) (Dimension, error) { return "", errNotImplemented }
func parseDate(name, s string) (Date, error)     { return Date{}, errNotImplemented }
func maxSpanDays(u Unit) int                     { panic("not implemented") }

// String renders YYYY-MM-DD.
func (d Date) String() string { panic("not implemented") }

// IsZero reports an unset date.
func (d Date) IsZero() bool { return d == Date{} }
