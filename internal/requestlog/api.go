package requestlog

import "time"

// ---------------------------------------------------------------------------
// GET /v8/management/observability/requests
// ---------------------------------------------------------------------------

// Filter selects entries. Zero values mean "no constraint".
// The management handler fills it straight from query parameters.
type Filter struct {
	Session string    // exact session ID                         ?session=
	KeyHash string    // 12-hex client key hash, or "-"           ?key=
	AuthID  string    // credential ID                            ?auth=
	Model   string    // exact upstream model                     ?model=
	Status  string    // "ok" | "failed" | "<http code>"          ?status=
	Verdict string    // csv of hit,partial,miss,cold,na          ?verdict=
	From    time.Time // RequestedAt >= From                   ?from=  (RFC3339 or unix ms)
	To      time.Time // RequestedAt <  To                     ?to=

	// Since is the live-refresh cursor: only entries with seq > Since.
	// The client sends back Page.Cursor from its previous response.   ?since=
	Since uint64
	// Before pages backwards for "load older": only entries with seq < Before. ?before=
	Before uint64
	// Limit caps returned entries: default 200, max 2000.               ?limit=
	Limit int
	// Asc returns oldest-first (session timeline). Default newest-first. ?order=asc
	Asc bool
}

// Page is the JSON body of GET /observability/requests.
//
//	{
//	  "enabled": true,
//	  "reason": "",                       // "disabled" | "home_mode" | "unavailable" when enabled=false
//	  "epoch": "k3j9x2",                  // changes when history restarts; client drops its rows and cursor
//	  "cursor": 18234,                    // highest seq in the store; send as ?since= next poll
//	  "oldest": 1001,                     // lowest seq still held
//	  "more": true,                       // more rows matched than limit (page with ?before=<last seq>)
//	  "keys": {"a1b2c3d4e5f6": "sk-...9f2c"},   // masked labels for key hashes present in entries
//	  "entries": [ Entry, ... ]
//	}
type Page struct {
	Enabled bool              `json:"enabled"`
	Reason  string            `json:"reason,omitempty"`
	Epoch   string            `json:"epoch"`
	Cursor  uint64            `json:"cursor"`
	Oldest  uint64            `json:"oldest"`
	More    bool              `json:"more"`
	Keys    map[string]string `json:"keys"`
	Entries []Entry           `json:"entries"`
}

// Entry is one upstream attempt.
//
//	{
//	  "seq": 18234,
//	  "request_id": "6f1c...-uuid",       // per attempt
//	  "trace_id": "a1b2c3d4",             // per inbound request; retries share it
//	  "at": "2026-10-18T17:02:11.412Z",
//	  "session_id": "6c1d...",
//	  "key_hash": "a1b2c3d4e5f6",
//	  "auth_id": "claude-max-1.json",
//	  "provider": "claude",
//	  "model": "claude-opus-4-5",
//	  "agent": "claude-cli",
//	  "stream": true,
//	  "status": 200,
//	  "failed": false,
//	  "error": "",                        // failed only, first 200 bytes
//	  "latency_ms": 18320,
//	  "ttft_ms": 1410,
//	  "tokens": {"input": 12, "cache_read": 81022, "cache_write": 1460, "output": 902, "reasoning": 0, "prompt": 82494},
//	  "cache": Cache
//	}
type Entry struct {
	Seq       uint64    `json:"seq"`
	RequestID string    `json:"request_id"`
	TraceID   string    `json:"trace_id,omitempty"`
	At        time.Time `json:"at"`
	SessionID string    `json:"session_id,omitempty"`
	KeyHash   string    `json:"key_hash"`
	AuthID    string    `json:"auth_id,omitempty"`
	Provider  string    `json:"provider"`
	Model     string    `json:"model"`
	Agent     string    `json:"agent"`
	Stream    bool      `json:"stream"`
	Status    int       `json:"status"`
	Failed    bool      `json:"failed"`
	Error     string    `json:"error,omitempty"`
	LatencyMs uint32    `json:"latency_ms"`
	TTFTMs    uint32    `json:"ttft_ms"`
	Tokens    Tokens    `json:"tokens"`
	Cache     Cache     `json:"cache"`
}

// Tokens: input is uncached input only; prompt = input + cache_read + cache_write.
type Tokens struct {
	Input      uint32 `json:"input"`
	CacheRead  uint32 `json:"cache_read"`
	CacheWrite uint32 `json:"cache_write"`
	Output     uint32 `json:"output"`
	Reasoning  uint32 `json:"reasoning"`
	Prompt     uint32 `json:"prompt"`
}

// Cache is the verdict, rendered.
//
//	hit:     {"verdict": "hit", "expected": 81000, "gap_s": 41, "ttl_s": 3600}
//	partial: {"verdict": "partial", "cause": "message_changed", "confidence": "high",
//	          "causes": ["message_changed"], "segment": {"kind": "message", "index": 14},
//	          "expected": 81000, "lost": 52310, "gap_s": 30, "ttl_s": 3600}
//	miss:    {"verdict": "miss", "cause": "idle_gap", "confidence": "high",
//	          "causes": ["idle_gap", "system_changed"], "segment": {"kind": "system", "index": 1},
//	          "expected": 81000, "lost": 81000, "gap_s": 4210, "ttl_s": 3600}
//	tools:   {"verdict": "miss", "cause": "tools_changed", "segment": {"kind": "tools", "index": 0},
//	          "tools": {"from": 37, "to": 38}, ...}
//	cold:    {"verdict": "cold"}
//	n/a:     {"verdict": "na", "reason": "no_session"}
//
// Cause strings: credential_changed, model_changed, tools_changed, system_changed,
// settings_changed, message_changed, instructions_changed, history_truncated,
// idle_gap, cache_key_changed, lookback_exceeded, no_breakpoint,
// volatile_changed, unexplained.
// NA reasons: failed, no_usage, no_session, unsupported, below_minimum, concurrent.
// Segment kinds: tools, system, settings, message, instructions, input.
type Cache struct {
	Verdict    string      `json:"verdict"`
	Reason     string      `json:"reason,omitempty"`
	Cause      string      `json:"cause,omitempty"`
	Confidence string      `json:"confidence,omitempty"`
	Causes     []string    `json:"causes,omitempty"`
	Segment    *SegmentRef `json:"segment,omitempty"`
	Tools      *ToolsDelta `json:"tools,omitempty"`
	Expected   uint32      `json:"expected,omitempty"`
	Lost       uint32      `json:"lost,omitempty"`
	GapSec     uint32      `json:"gap_s,omitempty"`
	TTLSec     uint32      `json:"ttl_s,omitempty"`
}

type SegmentRef struct {
	Kind  string `json:"kind"`
	Index int    `json:"index"`
}

type ToolsDelta struct {
	From int `json:"from"`
	To   int `json:"to"`
}

// ---------------------------------------------------------------------------
// GET /v8/management/observability/requests/sessions
// ---------------------------------------------------------------------------

// SessionFilter selects sessions by their entries. The drill-down timeline is
// NOT this endpoint: it is /observability/requests?session=<id>&order=asc.
type SessionFilter struct {
	KeyHash string    // ?key=
	AuthID  string    // ?auth=   sessions that used this credential at least once
	Model   string    // ?model=  sessions that used this model at least once
	From    time.Time // ?from=   sessions with any entry at or after From
	To      time.Time // ?to=
	Limit   int       // ?limit=  default 100, max 1000; ordered by last_at desc
}

// SessionPage is the JSON body of GET /observability/requests/sessions.
//
//	{
//	  "enabled": true,
//	  "epoch": "k3j9x2",
//	  "cursor": 18234,
//	  "keys": {"a1b2c3d4e5f6": "sk-...9f2c"},
//	  "sessions": [ Session, ... ]
//	}
type SessionPage struct {
	Enabled  bool              `json:"enabled"`
	Reason   string            `json:"reason,omitempty"`
	Epoch    string            `json:"epoch"`
	Cursor   uint64            `json:"cursor"`
	Keys     map[string]string `json:"keys"`
	Sessions []Session         `json:"sessions"`
}

// Session aggregates the entries of one session still held in the store.
//
//	{
//	  "session_id": "6c1d...",
//	  "key_hash": "a1b2c3d4e5f6",
//	  "agent": "claude-cli",
//	  "first_at": "2026-10-18T15:00:02Z",
//	  "last_at":  "2026-10-18T17:02:11Z",
//	  "requests": 214, "failed": 3,
//	  "models": ["claude-opus-4-5", "claude-haiku-4-5"],
//	  "auth_ids": ["claude-max-1.json"],
//	  "verdicts": {"hit": 190, "partial": 6, "miss": 4, "cold": 1, "na": 13},
//	  "tokens": {"input": 4210, "cache_read": 15100000, "cache_write": 410000, "output": 92000, "reasoning": 0, "prompt": 15514210},
//	  "hit_ratio": 0.973,                  // cache_read / prompt over judged entries
//	  "lost_tokens": 312000,               // sum of lost over partial+miss
//	  "causes": {"idle_gap": 3, "message_changed": 5, "tools_changed": 2},
//	  "top_cause": "message_changed",
//	  "last_miss": {"seq": 18101, "at": "...", "cause": "message_changed", "segment": {"kind": "message", "index": 0}}
//	}
type Session struct {
	SessionID  string         `json:"session_id"`
	KeyHash    string         `json:"key_hash"`
	Agent      string         `json:"agent"`
	FirstAt    time.Time      `json:"first_at"`
	LastAt     time.Time      `json:"last_at"`
	Requests   int            `json:"requests"`
	Failed     int            `json:"failed"`
	Models     []string       `json:"models"`
	AuthIDs    []string       `json:"auth_ids"`
	Verdicts   map[string]int `json:"verdicts"`
	Tokens     SessionTokens  `json:"tokens"`
	HitRatio   float64        `json:"hit_ratio"`
	LostTokens int64          `json:"lost_tokens"`
	Causes     map[string]int `json:"causes"`
	TopCause   string         `json:"top_cause,omitempty"`
	LastMiss   *MissRef       `json:"last_miss,omitempty"`
}

// SessionTokens are int64 sums (a long session overflows uint32).
type SessionTokens struct {
	Input      int64 `json:"input"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	Output     int64 `json:"output"`
	Reasoning  int64 `json:"reasoning"`
	Prompt     int64 `json:"prompt"`
}

type MissRef struct {
	Seq     uint64      `json:"seq"`
	At      time.Time   `json:"at"`
	Cause   string      `json:"cause"`
	Segment *SegmentRef `json:"segment,omitempty"`
}
