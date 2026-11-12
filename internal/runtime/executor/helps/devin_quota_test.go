package helps

import (
	"testing"
	"time"
)

func TestDevinUsageQuotaExhaustionPeriod(t *testing.T) {
	cases := []struct {
		name    string
		message string
		want    DevinQuotaPeriod
	}{
		{name: "weekly exact", message: "Your weekly usage quota has been exhausted.", want: DevinQuotaPeriodWeekly},
		{name: "daily exact", message: "Your daily usage quota has been exhausted.", want: DevinQuotaPeriodDaily},
		{name: "case-insensitive", message: "YOUR WEEKLY USAGE QUOTA HAS BEEN EXHAUSTED.", want: DevinQuotaPeriodWeekly},
		{name: "wrapped trailer text", message: "devin upstream error (failed_precondition): Your weekly usage quota has been exhausted.", want: DevinQuotaPeriodWeekly},
		{name: "weekly wins when both named", message: "Your daily and weekly usage quota has been exhausted.", want: DevinQuotaPeriodWeekly},
		{name: "monthly ACU out of scope", message: "User monthly ACU quota exhausted (trace ID: 12345)", want: DevinQuotaPeriodNone},
		{name: "rate limit without quota", message: "Rate limit exceeded. Please slow down.", want: DevinQuotaPeriodNone},
		{name: "weekly without exhausted", message: "Your weekly usage quota is nearly used up.", want: DevinQuotaPeriodNone},
		{name: "quota without period", message: "Usage quota exhausted.", want: DevinQuotaPeriodNone},
		{name: "weekly without quota", message: "Your weekly allowance has been exhausted.", want: DevinQuotaPeriodNone},
		{name: "empty", message: "", want: DevinQuotaPeriodNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := DevinUsageQuotaExhaustionPeriod(tc.message); got != tc.want {
				t.Fatalf("DevinUsageQuotaExhaustionPeriod(%q) = %v, want %v", tc.message, got, tc.want)
			}
		})
	}
}

func TestDevinQuotaRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 16, 15, 37, 9, 0, time.UTC)

	cases := []struct {
		name    string
		resetAt time.Time
		want    *time.Duration
	}{
		{name: "valid future weekly reset", resetAt: now.Add(72 * time.Hour), want: durationPtr(72 * time.Hour)},
		{name: "valid future daily reset", resetAt: now.Add(3 * time.Hour), want: durationPtr(3 * time.Hour)},
		{name: "past reset", resetAt: now.Add(-time.Minute), want: nil},
		{name: "reset exactly now", resetAt: now, want: nil},
		{name: "far future reset rejected", resetAt: now.Add(30 * 24 * time.Hour), want: nil},
		{name: "unknown reset", resetAt: time.Time{}, want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DevinQuotaRetryAfter(tc.resetAt, now)
			if tc.want == nil && got != nil {
				t.Fatalf("DevinQuotaRetryAfter(%v) = %v, want nil", tc.resetAt, *got)
			}
			if tc.want != nil && (got == nil || *got != *tc.want) {
				t.Fatalf("DevinQuotaRetryAfter(%v) = %v, want %v", tc.resetAt, got, *tc.want)
			}
		})
	}
}

func durationPtr(d time.Duration) *time.Duration { return &d }
