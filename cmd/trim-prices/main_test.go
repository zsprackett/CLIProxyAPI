package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory/prices"
)

func TestRun(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	var big strings.Builder
	big.WriteString(`{"sample_spec":{"mode":"chat"}`)
	for i := 0; i < 250; i++ {
		fmt.Fprintf(&big, `,"model-%d":{"mode":"chat","input_cost_per_token":1e-06,"output_cost_per_token":2e-06}`, i)
	}
	big.WriteString(`}`)

	tests := []struct {
		name    string
		in      string
		wantErr bool
	}{
		{"enough entries", big.String(), false},
		{"truncated file", `{"a":{"mode":"chat","input_cost_per_token":1e-06}}`, true},
		{"not json", `<html>`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := run(strings.NewReader(tt.in), &out, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				if out.Len() != 0 {
					t.Errorf("wrote %d bytes despite the error", out.Len())
				}
				return
			}
			cat, err := prices.Decode(out.Bytes())
			if err != nil {
				t.Fatalf("output does not decode: %v", err)
			}
			if cat.Source != prices.SourceBuiltin || len(cat.Entries) != 250 || !cat.Generated.Equal(now) {
				t.Errorf("catalog = %q, %d entries, %v", cat.Source, len(cat.Entries), cat.Generated)
			}
		})
	}
}
