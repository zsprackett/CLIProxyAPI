package usagehistory

import (
	"fmt"
	"net/url"
	"strings"
	"time"

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

const dateLayout = "2006-01-02"

// ParseQuery turns raw query parameters into a validated Query. It is the HTTP
// boundary: everything after it trusts the Query.
//
//	from, to  YYYY-MM-DD civil dates in tz, both inclusive. Either may be empty
//	          (defaults are filled by Report because they depend on "now").
//	tz        IANA name (America/Toronto). Empty = UTC.
//	bucket    hour | day | month. Empty = day.
//	group     model | provider | key | auth | agent | none. Empty = model.
//	provider, model, key, auth, agent   exact-match filters, each <= 200 bytes.
//
// ParseQuery never consults config or the clock.
func ParseQuery(values url.Values) (Query, error) {
	var errOne error
	one := func(name string) string {
		vs := values[name]
		if len(vs) > 1 && errOne == nil {
			errOne = fmt.Errorf("%s given more than once", name)
		}
		if len(vs) == 0 {
			return ""
		}
		return strings.TrimSpace(vs[0])
	}
	tz, bucket, group := one("tz"), one("bucket"), one("group")
	fromRaw, toRaw := one("from"), one("to")
	filter := Filter{
		Provider: one("provider"),
		Model:    one("model"),
		KeyHash:  one("key"),
		AuthID:   one("auth"),
		Agent:    one("agent"),
	}
	if errOne != nil {
		return Query{}, errOne
	}

	q := Query{Loc: time.UTC, Filter: filter}
	if tz != "" {
		loc, err := time.LoadLocation(tz)
		if err != nil || tz == "Local" {
			return Query{}, fmt.Errorf("tz must be an IANA time zone name, got %q", tz)
		}
		q.Loc = loc
	}
	var err error
	if q.Unit, err = parseUnit(bucket); err != nil {
		return Query{}, err
	}
	if q.Group, err = parseDimension(group); err != nil {
		return Query{}, err
	}
	if q.From, err = parseDate("from", fromRaw); err != nil {
		return Query{}, err
	}
	if q.To, err = parseDate("to", toRaw); err != nil {
		return Query{}, err
	}
	if !q.From.IsZero() && !q.To.IsZero() {
		if q.To.before(q.From) {
			return Query{}, fmt.Errorf("to (%s) is before from (%s)", q.To, q.From)
		}
		if limit := maxSpanDays(q.Unit); spanDays(q.From, q.To) > limit {
			return Query{}, fmt.Errorf("range is longer than %d days, the limit for bucket=%s", limit, q.Unit)
		}
	}
	for _, f := range []struct{ name, value string }{
		{"provider", filter.Provider}, {"model", filter.Model}, {"key", filter.KeyHash},
		{"auth", filter.AuthID}, {"agent", filter.Agent},
	} {
		if len(f.value) > maxFilterLen {
			return Query{}, fmt.Errorf("%s is longer than %d bytes", f.name, maxFilterLen)
		}
	}
	return q, nil
}

func parseUnit(s string) (Unit, error) {
	switch Unit(s) {
	case "":
		return UnitDay, nil
	case UnitHour, UnitDay, UnitMonth:
		return Unit(s), nil
	}
	return "", fmt.Errorf("bucket must be hour, day or month, got %q", s)
}

func parseDimension(s string) (Dimension, error) {
	switch Dimension(s) {
	case "":
		return DimModel, nil
	case DimNone, DimModel, DimProvider, DimKey, DimAuth, DimAgent:
		return Dimension(s), nil
	}
	return "", fmt.Errorf("group must be model, provider, key, auth, agent or none, got %q", s)
}

func parseDate(name, s string) (Date, error) {
	if s == "" {
		return Date{}, nil
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return Date{}, fmt.Errorf("%s must be a date YYYY-MM-DD, got %q", name, s)
	}
	return dateOf(t), nil
}

func maxSpanDays(u Unit) int {
	switch u {
	case UnitHour:
		return maxHourSpanDays
	case UnitMonth:
		return maxMonthSpanDays
	default:
		return maxDaySpanDays
	}
}

// spanDays counts the days of [from, to], both inclusive.
func spanDays(from, to Date) int {
	return int(to.utcMidnight().Sub(from.utcMidnight())/(24*time.Hour)) + 1
}

// dateOf returns the civil date of t in t's location.
func dateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{Year: y, Month: m, Day: d}
}

// addDays returns the civil date n days later (n may be negative).
func (d Date) addDays(n int) Date {
	return dateOf(time.Date(d.Year, d.Month, d.Day+n, 0, 0, 0, 0, time.UTC))
}

func (d Date) utcMidnight() time.Time { return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC) }

func (d Date) before(o Date) bool { return d.utcMidnight().Before(o.utcMidnight()) }

// String renders YYYY-MM-DD.
func (d Date) String() string { return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day) }

// IsZero reports an unset date.
func (d Date) IsZero() bool { return d == Date{} }
