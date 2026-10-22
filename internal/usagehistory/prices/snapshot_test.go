package prices

import "testing"

func TestBuiltinSnapshot(t *testing.T) {
	cat := Builtin()
	if cat.Source != SourceBuiltin {
		t.Errorf("Source = %q, want %q", cat.Source, SourceBuiltin)
	}
	if cat.Generated.IsZero() {
		t.Error("Generated is zero: the snapshot date is not recorded")
	}
	if len(cat.Entries) < minEntries {
		t.Errorf("len(Entries) = %d, want at least %d", len(cat.Entries), minEntries)
	}

	tests := []struct {
		name string
		key  string
		std  Rates
		long *Rates
	}{
		{
			name: "claude-opus-5-5", key: "claude-opus-5-5",
			std: Rates{Input: 4e-06, Output: 2e-05, Reasoning: 2e-05, CacheRead: 2e-07, CacheWrite5m: 5e-06, CacheWrite1h: 8e-06},
		},
		{
			name: "claude-sonnet-5", key: "claude-sonnet-5",
			std: Rates{Input: 2e-06, Output: 1e-05, Reasoning: 1e-05, CacheRead: 2e-07, CacheWrite5m: 2.5e-06, CacheWrite1h: 4e-06},
		},
		{
			name: "claude-sonnet-5-5", key: "claude-sonnet-5-5",
			std: Rates{Input: 2e-06, Output: 1e-05, Reasoning: 1e-05, CacheRead: 1e-07, CacheWrite5m: 2.5e-06, CacheWrite1h: 4e-06},
		},
		{
			name: "xai grok-4.7 with above-200k tier", key: "xai/grok-4.7",
			std:  Rates{Input: 2e-06, Output: 6e-06, Reasoning: 6e-06, CacheRead: 5e-07, CacheWrite5m: 2e-06, CacheWrite1h: 2e-06},
			long: &Rates{Input: 4e-06, Output: 1.2e-05, Reasoning: 1.2e-05, CacheRead: 1e-06, CacheWrite5m: 4e-06, CacheWrite1h: 4e-06},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, ok := cat.Entries[tt.key]
			if !ok {
				t.Fatalf("snapshot has no %q", tt.key)
			}
			if !sameRates(e.Std, tt.std) {
				t.Errorf("Std = %+v, want %+v", e.Std, tt.std)
			}
			switch {
			case tt.long == nil && e.Long != nil:
				t.Errorf("Long = %+v, want none", *e.Long)
			case tt.long != nil && e.Long == nil:
				t.Error("Long is missing")
			case tt.long != nil && !sameRates(*e.Long, *tt.long):
				t.Errorf("Long = %+v, want %+v", *e.Long, *tt.long)
			}
		})
	}
}

// sameRates compares the six rates exactly and ignores Derived.
func sameRates(a, b Rates) bool {
	a.Derived, b.Derived = nil, nil
	return a.Input == b.Input && a.Output == b.Output && a.Reasoning == b.Reasoning &&
		a.CacheRead == b.CacheRead && a.CacheWrite5m == b.CacheWrite5m && a.CacheWrite1h == b.CacheWrite1h
}
