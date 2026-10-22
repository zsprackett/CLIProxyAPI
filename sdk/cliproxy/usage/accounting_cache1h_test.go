package usage

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestTokenBreakdownWithCacheWrite1hSetsSubset(t *testing.T) {
	breakdown := NewIndependentTokenBreakdown(10, 20, 1000, 5, 0, 0).WithCacheWrite1h(900)
	if !breakdown.Valid() {
		t.Fatalf("breakdown is invalid: %+v", breakdown)
	}
	if breakdown.Input.CacheWriteTokens != 1000 || breakdown.Input.CacheWrite1hTokens != 900 {
		t.Fatalf("input = %+v, want cache writes 1000 with 900 at 1h", breakdown.Input)
	}
	if breakdown.Input.TotalTokens != 1030 || breakdown.TotalTokens != 1035 {
		t.Fatalf("the 1h subset changed the totals: %+v", breakdown)
	}
}

func TestTokenBreakdownWithCacheWrite1hIgnoresOutOfRange(t *testing.T) {
	base := NewIndependentTokenBreakdown(10, 0, 100, 5, 0, 0)
	for _, n := range []int64{-1, 101} {
		breakdown := base.WithCacheWrite1h(n)
		if breakdown.Input.CacheWrite1hTokens != 0 {
			t.Fatalf("WithCacheWrite1h(%d) subset = %d, want 0", n, breakdown.Input.CacheWrite1hTokens)
		}
		if !breakdown.Valid() {
			t.Fatalf("WithCacheWrite1h(%d) made the breakdown invalid: %+v", n, breakdown)
		}
	}
}

func TestTokenBreakdownValidBoundsCacheWrite1h(t *testing.T) {
	base := NewIndependentTokenBreakdown(10, 0, 100, 5, 0, 0)
	cases := []struct {
		name  string
		oneH  int64
		valid bool
	}{
		{name: "zero", oneH: 0, valid: true},
		{name: "all writes", oneH: 100, valid: true},
		{name: "negative", oneH: -1, valid: false},
		{name: "more than writes", oneH: 101, valid: false},
	}
	for _, tc := range cases {
		breakdown := base
		breakdown.Input.CacheWrite1hTokens = tc.oneH
		if got := breakdown.Valid(); got != tc.valid {
			t.Fatalf("%s: Valid() = %v, want %v", tc.name, got, tc.valid)
		}
	}
}

func TestTokenBreakdownJSONOmitsZeroCacheWrite1hAndReadsOldPayloads(t *testing.T) {
	encoded, err := json.Marshal(NewIndependentTokenBreakdown(10, 0, 100, 5, 0, 0))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "cache_write_1h_tokens") {
		t.Fatalf("zero 1h subset was serialized: %s", encoded)
	}

	old := `{"schema_version":2,"quality":"complete","total_tokens":115,` +
		`"input":{"total_tokens":110,"uncached_tokens":10,"cache_read_tokens":0,"cache_write_tokens":100},` +
		`"output":{"total_tokens":5,"non_reasoning_tokens":5,"reasoning_tokens":0},"unclassified_tokens":0}`
	var decoded TokenBreakdown
	if errUnmarshal := json.Unmarshal([]byte(old), &decoded); errUnmarshal != nil {
		t.Fatalf("unmarshal old payload: %v", errUnmarshal)
	}
	if !decoded.Valid() || decoded.Input.CacheWriteTokens != 100 || decoded.Input.CacheWrite1hTokens != 0 {
		t.Fatalf("old payload decoded as %+v", decoded)
	}

	withSplit := NewIndependentTokenBreakdown(10, 0, 100, 5, 0, 0).WithCacheWrite1h(60)
	encoded, err = json.Marshal(withSplit)
	if err != nil {
		t.Fatalf("marshal split: %v", err)
	}
	var roundTrip TokenBreakdown
	if errUnmarshal := json.Unmarshal(encoded, &roundTrip); errUnmarshal != nil {
		t.Fatalf("unmarshal split: %v", errUnmarshal)
	}
	if roundTrip != withSplit {
		t.Fatalf("round trip = %+v, want %+v", roundTrip, withSplit)
	}
}

func TestEnsureTokenBreakdownForProviderCarriesCacheWrite1h(t *testing.T) {
	detail := EnsureTokenBreakdownForProvider(Detail{
		InputTokens:           10,
		CacheReadTokens:       20,
		CacheCreationTokens:   1000,
		CacheCreation1hTokens: 900,
		OutputTokens:          5,
	}, "claude", "claude")
	if !detail.TokenBreakdown.Valid() {
		t.Fatalf("breakdown is invalid: %+v", detail.TokenBreakdown)
	}
	if detail.TokenBreakdown.Input.CacheWriteTokens != 1000 || detail.TokenBreakdown.Input.CacheWrite1hTokens != 900 {
		t.Fatalf("input = %+v, want 1000 writes with 900 at 1h", detail.TokenBreakdown.Input)
	}
}
