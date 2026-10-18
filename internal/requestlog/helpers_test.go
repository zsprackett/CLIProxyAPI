package requestlog

import (
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/requestlog/prompt"
)

const (
	testToolA = `{"name":"read","description":"read a file","input_schema":{"type":"object"}}`
	testToolB = `{"name":"write","description":"write a file","input_schema":{"type":"object"}}`
	testInA   = `{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}`
	testInB   = `{"type":"message","role":"user","content":[{"type":"input_text","text":"next"}]}`
)

// claudeOpts shapes a Claude Messages test body.
type claudeOpts struct {
	cch      string
	tools    []string
	thinking string
	system   string
	messages []string
	ttl1h    bool
	noCache  bool
}

// claudeTestBody builds a body in the shape the proxy sends upstream: a billing
// header block, a cached system prompt, tools, and the moving breakpoint on the
// last message.
func claudeTestBody(o claudeOpts) []byte {
	if o.cch == "" {
		o.cch = "cch0"
	}
	if o.system == "" {
		o.system = "You are Claude Code."
	}
	if len(o.tools) == 0 {
		o.tools = []string{testToolA}
	}
	ttl := ""
	if o.ttl1h {
		ttl = `,"ttl":"1h"`
	}
	cc := func() string {
		if o.noCache {
			return ""
		}
		return `,"cache_control":{"type":"ephemeral"` + ttl + `}`
	}
	var b strings.Builder
	b.WriteString(`{"model":"claude-opus-4-5","max_tokens":1024,"tools":[` + strings.Join(o.tools, ",") + `],`)
	b.WriteString(`"system":[{"type":"text","text":"x-anthropic-billing-header: cc_version=2.1.3; cch=` + o.cch + `"` + cc() + `},`)
	b.WriteString(`{"type":"text","text":"` + o.system + `"` + cc() + `}],`)
	if o.thinking != "" {
		b.WriteString(`"thinking":` + o.thinking + `,`)
	}
	b.WriteString(`"messages":[`)
	for i, m := range o.messages {
		if i > 0 {
			b.WriteString(",")
		}
		mark := ""
		if i == len(o.messages)-1 {
			mark = cc()
		}
		b.WriteString(`{"role":"user","content":[{"type":"text","text":"` + m + `"` + mark + `}]}`)
	}
	b.WriteString(`]}`)
	return []byte(b.String())
}

func claudePrintOf(o claudeOpts) *prompt.Print {
	return prompt.Of(claudeTestBody(o), prompt.FormatClaudeMessages)
}

func messagesN(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("m%d", i)
	}
	return out
}

type codexOpts struct {
	instructions string
	cacheKey     string
	prevID       string
	tools        []string
	input        []string
}

func codexTestBody(o codexOpts) []byte {
	if o.instructions == "" {
		o.instructions = "be brief"
	}
	if len(o.tools) == 0 {
		o.tools = []string{testToolA}
	}
	var b strings.Builder
	b.WriteString(`{"model":"gpt-5","store":false,"instructions":` + fmt.Sprintf("%q", o.instructions) + `,`)
	if o.cacheKey != "" {
		b.WriteString(`"prompt_cache_key":"` + o.cacheKey + `",`)
	}
	if o.prevID != "" {
		b.WriteString(`"previous_response_id":"` + o.prevID + `",`)
	}
	b.WriteString(`"tools":[` + strings.Join(o.tools, ",") + `],"input":[` + strings.Join(o.input, ",") + `]}`)
	return []byte(b.String())
}

func codexPrintOf(o codexOpts) *prompt.Print {
	return prompt.Of(codexTestBody(o), prompt.FormatCodexResponses)
}

var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// obsAt builds a successful observation with known tokens.
func obsAt(at time.Time, session, model, auth string, uncached, read, write int64, p *prompt.Print) observation {
	return observation{
		sessionID:   session,
		model:       model,
		authID:      auth,
		requestedAt: at,
		touchedAt:   at.Add(time.Second),
		tokens:      tokenCounts{uncached: uncached, read: read, write: write, output: 10},
		tokensKnown: true,
		print:       p,
	}
}

func laneAt(model, auth string, touched time.Time, cachedPrefix int64, ttl time.Duration, p *prompt.Print) lane {
	return lane{model: model, authID: auth, touchedAt: touched, cachedPrefix: cachedPrefix, ttl: ttl, print: p}
}
