package quotaforecast

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory"
)

// fakeUsage is a usageReader that returns a fixed result and records each query.
type fakeUsage struct {
	result  usagehistory.HourlyResult
	queries []usagehistory.HourlyQuery
}

func (f *fakeUsage) Hourly(_ context.Context, q usagehistory.HourlyQuery) usagehistory.HourlyResult {
	f.queries = append(f.queries, q)
	return f.result
}

// useFakeUsage points usageSource at f for the rest of the test.
func useFakeUsage(t *testing.T, f *fakeUsage) {
	t.Helper()
	previous := usageSource
	usageSource = func() usageReader { return f }
	t.Cleanup(func() { usageSource = previous })
}

func enabledUsage(rows ...usagehistory.HourlyRow) *fakeUsage {
	return &fakeUsage{result: usagehistory.HourlyResult{Enabled: true, Rows: rows}}
}

func TestLoadUsage(t *testing.T) {
	hour := testNow.Truncate(time.Hour).Unix()
	row := usagehistory.HourlyRow{
		Hour: hour, Provider: "claude", AuthID: "a", Model: "m", KeyHash: "k", Agent: "claude-code",
		Requests: 3, Failures: 1,
		Tokens: usagehistory.Tokens{Uncached: 100, CacheRead: 1000, CacheWrite5m: 20, CacheWrite1h: 30, Output: 40, Reasoning: 5, Unclassified: 7},
	}
	unattributed := row
	unattributed.AuthID = "-"
	bucket := UsageBucket{Hour: hour, AuthID: "a", Model: "m", KeyHash: "k", Agent: "claude-code", Requests: 3, Tokens: 202}
	tests := []struct {
		name       string
		reader     *fakeUsage
		want       []UsageBucket
		wantReason string
	}{
		{name: "projects rows to buckets without cache reads", reader: enabledUsage(row), want: []UsageBucket{bucket}},
		{name: "drops rows without a credential", reader: enabledUsage(unattributed, row), want: []UsageBucket{bucket}},
		{name: "disabled", reader: &fakeUsage{result: usagehistory.HourlyResult{Reason: usagehistory.ReasonDisabled}}, wantReason: "usage_history_disabled"},
		{name: "home mode", reader: &fakeUsage{result: usagehistory.HourlyResult{Reason: usagehistory.ReasonHomeMode}}, wantReason: "usage_history_home_mode"},
		{name: "unavailable", reader: &fakeUsage{result: usagehistory.HourlyResult{Reason: usagehistory.ReasonUnavailable}}, wantReason: "usage_history_unavailable"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			useFakeUsage(t, tt.reader)
			since := testNow.Add(-14 * 24 * time.Hour)
			got, reason := loadUsage(context.Background(), since)
			if reason != tt.wantReason || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("loadUsage = %+v, %q; want %+v, %q", got, reason, tt.want, tt.wantReason)
			}
			wantQuery := usagehistory.HourlyQuery{Since: since, Providers: []string{"claude", "codex", "devin", "xai"}}
			if len(tt.reader.queries) != 1 || !reflect.DeepEqual(tt.reader.queries[0], wantQuery) {
				t.Fatalf("queries = %+v, want [%+v]", tt.reader.queries, wantQuery)
			}
		})
	}
}
