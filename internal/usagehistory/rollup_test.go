package usagehistory

import (
	"context"
	"strings"
	"testing"
	"time"

	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

var rollupNow = time.Date(2026, time.October, 22, 15, 42, 0, 0, time.UTC)

func TestRollupTokenMapping(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		detail   coreusage.Detail
		want     Tokens
	}{
		{
			name:     "claude with 1h cache writes",
			provider: "claude",
			detail: coreusage.Detail{
				InputTokens: 100, CacheReadTokens: 5000, CacheCreationTokens: 3000,
				CacheCreation1hTokens: 2000, OutputTokens: 400,
			},
			want: Tokens{Uncached: 100, CacheRead: 5000, CacheWrite5m: 1000, CacheWrite1h: 2000, Output: 400},
		},
		{
			name:     "claude with a valid breakdown already set",
			provider: "claude",
			detail: coreusage.Detail{
				TokenBreakdown: coreusage.NewIndependentTokenBreakdown(10, 20, 30, 40, 0, 0).WithCacheWrite1h(30),
			},
			want: Tokens{Uncached: 10, CacheRead: 20, CacheWrite1h: 30, Output: 40},
		},
		{
			name:     "codex subset: cache inside input, reasoning inside output",
			provider: "codex",
			detail:   coreusage.Detail{InputTokens: 1000, CacheReadTokens: 800, OutputTokens: 300, ReasoningTokens: 200},
			want:     Tokens{Uncached: 200, CacheRead: 800, Output: 100, Reasoning: 200},
		},
		{
			name:     "openai-compatible subset",
			provider: "openai-compatible-foo",
			detail:   coreusage.Detail{InputTokens: 50, OutputTokens: 20},
			want:     Tokens{Uncached: 50, Output: 20},
		},
		{
			name:     "gemini separate reasoning",
			provider: "gemini",
			detail:   coreusage.Detail{InputTokens: 1000, CacheReadTokens: 600, OutputTokens: 70, ReasoningTokens: 30},
			want:     Tokens{Uncached: 400, CacheRead: 600, Output: 70, Reasoning: 30},
		},
		{
			name:     "unclassified provider keeps only the total",
			provider: "devin",
			detail:   coreusage.Detail{TotalTokens: 1234},
			want:     Tokens{Unclassified: 1234},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, counters, ok := rollupOf(context.Background(), coreusage.Record{
				Provider: c.provider, Model: "m", RequestedAt: rollupNow, Detail: c.detail,
			}, rollupNow)
			if !ok {
				t.Fatal("record skipped")
			}
			if counters.Tokens != c.want {
				t.Fatalf("tokens = %+v, want %+v", counters.Tokens, c.want)
			}
			if counters.Requests != 1 || counters.Failures != 0 {
				t.Fatalf("counters = %+v", counters)
			}
		})
	}
}

func TestRollupKey(t *testing.T) {
	ctx := internallogging.WithClientRequestMetadata(context.Background(),
		internallogging.ClientRequestMetadata{UserAgent: "claude-cli/2.1.3 (external, cli)"})
	rec := coreusage.Record{
		Provider:    " Claude ",
		AuthID:      "claude-a.json",
		Model:       "claude-opus-5-5",
		APIKey:      "sk-client",
		RequestedAt: time.Date(2026, time.October, 22, 9, 59, 59, 0, time.FixedZone("x", -4*3600)),
		Latency:     1500 * time.Millisecond,
		Failed:      true,
	}
	key, counters, ok := rollupOf(ctx, rec, rollupNow)
	if !ok {
		t.Fatal("record skipped")
	}
	want := Key{
		Hour:     time.Date(2026, time.October, 22, 13, 0, 0, 0, time.UTC).Unix(),
		Provider: "claude",
		AuthID:   "claude-a.json",
		Model:    "claude-opus-5-5",
		KeyHash:  HashClientKey("sk-client"),
		Agent:    "claude-cli",
	}
	if key != want {
		t.Fatalf("key = %+v, want %+v", key, want)
	}
	if counters.Failures != 1 || counters.LatencyMsSum != 1500 {
		t.Fatalf("counters = %+v", counters)
	}
}

func TestRollupKeyDefaults(t *testing.T) {
	long := strings.Repeat("m", maxDimLen+10)
	key, _, _ := rollupOf(context.Background(), coreusage.Record{Alias: long}, rollupNow)
	want := Key{
		Hour:     time.Date(2026, time.October, 22, 15, 0, 0, 0, time.UTC).Unix(),
		Provider: "unknown",
		AuthID:   "-",
		Model:    long[:maxDimLen],
		KeyHash:  "-",
		Agent:    "-",
	}
	if key != want {
		t.Fatalf("key = %+v, want %+v", key, want)
	}
	key, _, _ = rollupOf(context.Background(), coreusage.Record{}, rollupNow)
	if key.Model != "unknown" {
		t.Fatalf("model = %q, want unknown", key.Model)
	}
}

func TestRollupSkipsGenerateFalse(t *testing.T) {
	if _, _, ok := rollupOf(context.Background(), coreusage.Record{Generate: coreusage.GenerateFlag(false)}, rollupNow); ok {
		t.Fatal("Generate=false record was counted")
	}
	if _, _, ok := rollupOf(context.Background(), coreusage.Record{Generate: coreusage.GenerateFlag(true)}, rollupNow); !ok {
		t.Fatal("Generate=true record was skipped")
	}
}

func TestRollupLongContextThreshold(t *testing.T) {
	cases := []struct {
		prompt int64
		want   bool
	}{
		{LongContextThreshold - 1, false},
		{LongContextThreshold, false},
		{LongContextThreshold + 1, true},
	}
	for _, c := range cases {
		// Prompt = uncached 1 + cache read (prompt-1).
		key, _, _ := rollupOf(context.Background(), coreusage.Record{
			Provider: "claude",
			Detail:   coreusage.Detail{InputTokens: 1, CacheReadTokens: c.prompt - 1, OutputTokens: 10},
		}, rollupNow)
		if key.LongCtx != c.want {
			t.Errorf("prompt %d: LongCtx = %v, want %v", c.prompt, key.LongCtx, c.want)
		}
	}
}

func TestPendingAddSwapRestore(t *testing.T) {
	p := newPending()
	k := Key{Hour: 1, Provider: "claude", AuthID: "-", Model: "m", KeyHash: "-", Agent: "-"}
	p.add(k, Counters{Requests: 1, Tokens: Tokens{Output: 5}})
	p.add(k, Counters{Requests: 1, Tokens: Tokens{Output: 7}})
	batch, dropped := p.swap()
	if dropped != 0 || len(batch) != 1 || batch[k].Requests != 2 || batch[k].Tokens.Output != 12 {
		t.Fatalf("batch = %+v dropped %d", batch, dropped)
	}
	p.add(k, Counters{Requests: 1})
	p.restore(batch)
	again, _ := p.swap()
	if again[k].Requests != 3 || again[k].Tokens.Output != 12 {
		t.Fatalf("after restore = %+v", again[k])
	}
	p.add(k, Counters{Requests: 1})
	p.discard()
	if empty, _ := p.swap(); len(empty) != 0 {
		t.Fatalf("discard left %d keys", len(empty))
	}
}

func TestPendingBound(t *testing.T) {
	p := newPending()
	p.limit = 2
	for i := int64(0); i < 4; i++ {
		p.add(Key{Hour: i}, Counters{Requests: 1})
	}
	p.add(Key{Hour: 0}, Counters{Requests: 1}) // existing key: always merged
	batch, dropped := p.swap()
	if len(batch) != 2 || dropped != 2 || batch[Key{Hour: 0}].Requests != 2 {
		t.Fatalf("batch %d keys, dropped %d, key0 %+v", len(batch), dropped, batch[Key{Hour: 0}])
	}
}
