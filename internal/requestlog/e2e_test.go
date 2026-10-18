package requestlog

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/requestlog/prompt"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/runtime/executor/helps"
	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
	"github.com/tidwall/gjson"
)

// forwarder delivers the records of one test session from the global usage
// manager to a test service, and signals each delivery.
type forwarder struct {
	svc       *Service
	session   string
	delivered chan struct{}
}

func (f *forwarder) HandleUsage(ctx context.Context, record coreusage.Record) {
	if record.SessionID != f.session {
		return
	}
	f.svc.HandleUsage(ctx, record)
	f.delivered <- struct{}{}
}

func (f *forwarder) wait(t *testing.T) {
	t.Helper()
	select {
	case <-f.delivered:
	case <-time.After(3 * time.Second):
		t.Fatal("usage record was not delivered")
	}
}

// TestEndToEndClaudeSession drives three turns of one Claude Code session through a
// real UsageReporter (capture at request time, print at publish, delivery through
// the usage manager) and reads the verdicts back through the public Query JSON.
func TestEndToEndClaudeSession(t *testing.T) {
	defer prompt.SetCaptureEnabled(false)
	cfg := testConfig(t, true)
	s, manager := testService(cfg)
	s.now = time.Now
	s.Start(context.Background(), manager)
	defer s.Stop(context.Background())

	const sessionID = "e2e-claude-session"
	fwd := &forwarder{svc: s, session: sessionID, delivered: make(chan struct{}, 1)}
	coreusage.RegisterNamedPlugin("requestlog-e2e-forwarder", fwd)
	ctx := internallogging.WithClientRequestMetadata(context.Background(), internallogging.ClientRequestMetadata{
		SessionID: sessionID,
		UserAgent: "claude-cli/2.1.3 (external, cli)",
	})

	turn := func(body []byte, usageJSON string) {
		t.Helper()
		reporter := helps.NewUsageReporter(ctx, "claude", "claude-opus-4-5", nil)
		reporter.CapturePrompt(body, "claude")
		reporter.Publish(ctx, helps.ParseClaudeUsage([]byte(usageJSON)))
		fwd.wait(t)
	}

	turn(claudeTestBody(claudeOpts{messages: []string{"one"}}),
		`{"usage":{"input_tokens":12,"output_tokens":40,"cache_read_input_tokens":0,"cache_creation_input_tokens":5000}}`)
	turn(claudeTestBody(claudeOpts{messages: []string{"one", "two"}}),
		`{"usage":{"input_tokens":9,"output_tokens":30,"cache_read_input_tokens":5000,"cache_creation_input_tokens":300}}`)
	turn(claudeTestBody(claudeOpts{tools: []string{testToolA, testToolB}, messages: []string{"one", "two", "three"}}),
		`{"usage":{"input_tokens":11,"output_tokens":25,"cache_read_input_tokens":0,"cache_creation_input_tokens":5400}}`)

	raw, err := json.Marshal(s.Query(Filter{Session: sessionID, Asc: true}))
	if err != nil {
		t.Fatal(err)
	}
	page := gjson.ParseBytes(raw)
	if !page.Get("enabled").Bool() || page.Get("entries.#").Int() != 3 {
		t.Fatalf("page = %s", raw)
	}
	entries := page.Get("entries").Array()
	for i, want := range []string{"cold", "hit", "miss"} {
		if got := entries[i].Get("cache.verdict").String(); got != want {
			t.Fatalf("entry %d verdict = %q, want %q: %s", i, got, want, entries[i].Raw)
		}
	}
	first := entries[0]
	if first.Get("tokens.cache_write").Int() != 5000 || first.Get("tokens.cache_read").Int() != 0 || first.Get("tokens.input").Int() != 12 || first.Get("tokens.prompt").Int() != 5012 {
		t.Fatalf("a cache write must not be reported as a read: %s", first.Get("tokens").Raw)
	}
	if first.Get("agent").String() != "claude-cli" || first.Get("provider").String() != "claude" || first.Get("model").String() != "claude-opus-4-5" || first.Get("session_id").String() != sessionID {
		t.Fatalf("first entry = %s", first.Raw)
	}
	hit := entries[1].Get("cache")
	if hit.Get("expected").Int() != 5000 || hit.Get("ttl_s").Int() != 300 || hit.Get("lost").Exists() {
		t.Fatalf("hit = %s", hit.Raw)
	}
	miss := entries[2].Get("cache")
	if miss.Get("cause").String() != "tools_changed" || miss.Get("confidence").String() != "high" ||
		miss.Get("segment.kind").String() != "tools" || miss.Get("tools.from").Int() != 1 || miss.Get("tools.to").Int() != 2 ||
		miss.Get("expected").Int() != 5300 || miss.Get("lost").Int() != 5300 || miss.Get("causes").String() != `["tools_changed"]` {
		t.Fatalf("miss = %s", miss.Raw)
	}

	sessions, err := json.Marshal(s.Sessions(SessionFilter{}))
	if err != nil {
		t.Fatal(err)
	}
	sess := gjson.GetBytes(sessions, `sessions.#(session_id=="`+sessionID+`")`)
	if sess.Get("requests").Int() != 3 || sess.Get("verdicts.hit").Int() != 1 || sess.Get("top_cause").String() != "tools_changed" || sess.Get("last_miss.seq").Int() != 3 {
		t.Fatalf("session = %s", sess.Raw)
	}
}

// TestTokenMappingFromParsers pins the mapping from the usage parsers' Detail
// (including the CachedTokens copy of the creation count) to the log's buckets,
// for Claude non-stream, Claude stream and Codex.
func TestTokenMappingFromParsers(t *testing.T) {
	claudeWrite := helps.ParseClaudeUsage([]byte(`{"usage":{"input_tokens":12,"output_tokens":40,"cache_read_input_tokens":0,"cache_creation_input_tokens":5000}}`))
	if claudeWrite.CachedTokens != 5000 {
		t.Fatal("test premise: the parser copies the creation count into CachedTokens")
	}
	got, known := tokensFromDetail(claudeWrite)
	if !known || got != (tokenCounts{uncached: 12, read: 0, write: 5000, output: 40}) {
		t.Fatalf("claude non-stream write = %+v", got)
	}

	claudeStream, ok := helps.ParseClaudeStreamUsage([]byte(`data: {"type":"message_start","message":{"usage":{"input_tokens":9,"output_tokens":1,"cache_read_input_tokens":5000,"cache_creation_input_tokens":300}}}`))
	if !ok {
		t.Fatal("stream usage not parsed")
	}
	got, known = tokensFromDetail(claudeStream)
	if !known || got != (tokenCounts{uncached: 9, read: 5000, write: 300, output: 1}) {
		t.Fatalf("claude stream = %+v", got)
	}

	codex, ok := helps.ParseCodexUsage([]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":10240,"output_tokens":50,"total_tokens":10290,"input_tokens_details":{"cached_tokens":9984},"output_tokens_details":{"reasoning_tokens":7}}}}`))
	if !ok {
		t.Fatal("codex usage not parsed")
	}
	got, known = tokensFromDetail(codex)
	if !known || got != (tokenCounts{uncached: 256, read: 9984, write: 0, output: 50, reasoning: 7}) {
		t.Fatalf("codex = %+v", got)
	}
}
