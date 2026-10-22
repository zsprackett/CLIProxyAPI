package usagehistory

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestParseQueryDefaults(t *testing.T) {
	q, err := ParseQuery(url.Values{})
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}
	if q.Loc != time.UTC || q.Unit != UnitDay || q.Group != DimModel {
		t.Fatalf("defaults = loc %v unit %q group %q, want UTC day model", q.Loc, q.Unit, q.Group)
	}
	if !q.From.IsZero() || !q.To.IsZero() || q.Filter != (Filter{}) {
		t.Fatalf("defaults should leave dates and filter empty: %+v", q)
	}
}

func TestParseQueryFull(t *testing.T) {
	v := url.Values{
		"from": {"2026-03-01"}, "to": {"2026-03-31"}, "tz": {"America/Toronto"},
		"bucket": {"hour"}, "group": {"key"},
		"provider": {" claude "}, "model": {"claude-opus-5-5"}, "key": {"abcdef012345"},
		"auth": {"a.json"}, "agent": {"claude-cli"},
	}
	q, err := ParseQuery(v)
	if err != nil {
		t.Fatalf("ParseQuery: %v", err)
	}
	if q.Loc.String() != "America/Toronto" || q.Unit != UnitHour || q.Group != DimKey {
		t.Fatalf("got loc %v unit %q group %q", q.Loc, q.Unit, q.Group)
	}
	if q.From != (Date{2026, time.March, 1}) || q.To != (Date{2026, time.March, 31}) {
		t.Fatalf("dates = %v..%v", q.From, q.To)
	}
	want := Filter{Provider: "claude", Model: "claude-opus-5-5", KeyHash: "abcdef012345", AuthID: "a.json", Agent: "claude-cli"}
	if q.Filter != want {
		t.Fatalf("filter = %+v, want %+v", q.Filter, want)
	}
	if q.From.String() != "2026-03-01" {
		t.Fatalf("Date.String = %q", q.From.String())
	}
}

func TestParseQueryAcceptsEveryUnitAndGroup(t *testing.T) {
	for _, u := range []string{"hour", "day", "month"} {
		if _, err := ParseQuery(url.Values{"bucket": {u}}); err != nil {
			t.Errorf("bucket %q: %v", u, err)
		}
	}
	for _, g := range []string{"model", "provider", "key", "auth", "agent", "none"} {
		q, err := ParseQuery(url.Values{"group": {g}})
		if err != nil || string(q.Group) != g {
			t.Errorf("group %q: got %q, %v", g, q.Group, err)
		}
	}
}

func TestParseQuerySpanLimits(t *testing.T) {
	cases := []struct {
		bucket, from, to string
		ok               bool
	}{
		{"hour", "2026-01-01", "2026-01-31", true},  // 31 days
		{"hour", "2026-01-01", "2026-02-01", false}, // 32 days
		{"day", "2024-01-01", "2027-01-04", true},   // 1100 days
		{"day", "2024-01-01", "2027-01-05", false},  // 1101 days
		{"month", "2016-01-01", "2026-01-07", true}, // 3660 days
		{"month", "2016-01-01", "2026-01-08", false},
		{"day", "2026-01-05", "2026-01-05", true}, // one day
	}
	for _, c := range cases {
		_, err := ParseQuery(url.Values{"bucket": {c.bucket}, "from": {c.from}, "to": {c.to}})
		if (err == nil) != c.ok {
			t.Errorf("%s %s..%s: err = %v, want ok=%v", c.bucket, c.from, c.to, err, c.ok)
		}
	}
}

func TestParseQueryRejects(t *testing.T) {
	long := strings.Repeat("x", maxFilterLen+1)
	cases := []struct {
		name   string
		values url.Values
		want   string
	}{
		{"bad from", url.Values{"from": {"2026-13-01"}}, "from"},
		{"bad to", url.Values{"to": {"yesterday"}}, "to"},
		{"to before from", url.Values{"from": {"2026-03-02"}, "to": {"2026-03-01"}}, "before"},
		{"unknown tz", url.Values{"tz": {"Mars/Olympus"}}, "tz"},
		{"unknown bucket", url.Values{"bucket": {"week"}}, "bucket"},
		{"unknown group", url.Values{"group": {"color"}}, "group"},
		{"span", url.Values{"bucket": {"hour"}, "from": {"2026-01-01"}, "to": {"2026-03-01"}}, "31"},
		{"repeated", url.Values{"model": {"a", "b"}}, "model"},
		{"repeated tz", url.Values{"tz": {"UTC", "UTC"}}, "tz"},
		{"provider too long", url.Values{"provider": {long}}, "provider"},
		{"model too long", url.Values{"model": {long}}, "model"},
		{"key too long", url.Values{"key": {long}}, "key"},
		{"auth too long", url.Values{"auth": {long}}, "auth"},
		{"agent too long", url.Values{"agent": {long}}, "agent"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseQuery(c.values)
			if err == nil {
				t.Fatal("ParseQuery accepted the query")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not name %q", err, c.want)
			}
		})
	}
}
