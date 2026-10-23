package usagehistory

import "time"

// ---------------------------------------------------------------------------
// Tokens: seven disjoint buckets. Invariant: every token of a record lands in
// exactly one bucket, so Total() is the record's total and stacked charts never
// double count. Output EXCLUDES reasoning (unlike TokenBreakdown.Output.Total).
// ---------------------------------------------------------------------------

// Tokens is the disjoint token breakdown of one rollup cell.
type Tokens struct {
	Uncached     int64 `json:"uncached"`       // Input.UncachedTokens
	CacheRead    int64 `json:"cache_read"`     // Input.CacheReadTokens
	CacheWrite5m int64 `json:"cache_write_5m"` // Input.CacheWriteTokens - Input.CacheWrite1hTokens (default-TTL writes, incl. every non-Anthropic write)
	CacheWrite1h int64 `json:"cache_write_1h"` // Input.CacheWrite1hTokens (Anthropic ephemeral_1h)
	Output       int64 `json:"output"`         // Output.NonReasoningTokens
	Reasoning    int64 `json:"reasoning"`      // Output.ReasoningTokens
	Unclassified int64 `json:"unclassified"`   // UnclassifiedTokens (devin, unknown, inconsistent); never priced
}

// Total is the sum of the seven buckets.
func (t Tokens) Total() int64 { panic("not implemented") }

// Add returns t + o field by field.
func (t Tokens) Add(o Tokens) Tokens { panic("not implemented") }

// CostByType is Tokens priced: USD per bucket. Unclassified has no price, so it
// is absent here and counted in Cell.UnpricedTokens instead.
type CostByType struct {
	Uncached     float64 `json:"uncached"`
	CacheRead    float64 `json:"cache_read"`
	CacheWrite5m float64 `json:"cache_write_5m"`
	CacheWrite1h float64 `json:"cache_write_1h"`
	Output       float64 `json:"output"`
	Reasoning    float64 `json:"reasoning"`
}

// Sum is the cell's estimated API-price cost.
func (c CostByType) Sum() float64 { panic("not implemented") }

// ---------------------------------------------------------------------------
// Query: the typed form of ?from&to&tz&bucket&group&provider&model&key&auth&agent.
// Built only by ParseQuery; every field is already validated.
// ---------------------------------------------------------------------------

// Unit is a report bucket size in the client's time zone.
type Unit string

const (
	UnitHour  Unit = "hour"
	UnitDay   Unit = "day"
	UnitMonth Unit = "month"
)

// Dimension is a rollup key column a report can group or filter by.
type Dimension string

const (
	DimNone     Dimension = "none" // one group, key "all"
	DimModel    Dimension = "model"
	DimProvider Dimension = "provider"
	DimKey      Dimension = "key"   // client key hash
	DimAuth     Dimension = "auth"  // credential ID
	DimAgent    Dimension = "agent" // AgentFamily(User-Agent)
)

// Date is a civil date in the query's time zone.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// Filter narrows the rows. Empty fields do not constrain. Exact match.
type Filter struct {
	Provider string
	Model    string
	KeyHash  string
	AuthID   string
	Agent    string
}

// Query is a validated report request.
type Query struct {
	From  Date // inclusive; zero = To - 29 days (filled by Report from s.now in Loc)
	To    Date // inclusive; zero = today in Loc
	Loc   *time.Location
	Unit  Unit
	Group Dimension
	Filter
}

// ---------------------------------------------------------------------------
// Report: the JSON body of GET /v8/management/observability/usage/history.
// ---------------------------------------------------------------------------

// Reason says why a report or price list is empty.
type Reason string

const (
	ReasonNone        Reason = ""
	ReasonDisabled    Reason = "disabled"
	ReasonHomeMode    Reason = "home_mode"
	ReasonUnavailable Reason = "unavailable" // auth dir unresolvable, DB unopenable, or a read failed
)

// Cell is the aggregate of one (bucket, group), one group overall, or the total.
type Cell struct {
	Tokens    Tokens  `json:"tokens"`
	Requests  int64   `json:"requests"` // upstream attempts (retries count), Generate=false excluded
	Failures  int64   `json:"failures"`
	LatencyMs float64 `json:"latency_ms"` // mean over all attempts incl. failures; 0 when Requests == 0
	// CostUSD is nil iff the cell has tokens and none of them could be priced.
	// Otherwise it is the sum of the priced part; UnpricedTokens says how much is missing.
	CostUSD        *float64    `json:"cost_usd"`
	CostByType     *CostByType `json:"cost_by_type"` // nil exactly when CostUSD is nil
	UnpricedTokens int64       `json:"unpriced_tokens"` // tokens of unpriced models + all unclassified tokens
}

// GroupCell is a Cell labelled with its group value (model id, provider, key
// hash, auth ID, agent, or "all").
type GroupCell struct {
	Key string `json:"key"`
	Cell
}

// Bucket is one local-time bucket. Start/End are RFC3339 with the local UTC
// offset of that instant ("2026-11-01T01:00:00-05:00"), so the panel renders
// local wall-clock labels without doing any time-zone math. Every bucket of the
// range is present (empty Groups for no traffic) so the x axis is continuous.
type Bucket struct {
	Start  string      `json:"start"`
	End    string      `json:"end"`
	Groups []GroupCell `json:"groups"` // only groups with traffic, sorted by key
}

// Options lists the distinct values present in the range, ignoring the filter,
// so the panel's filter selects offer exactly what exists.
type Options struct {
	Provider []string `json:"provider"`
	Model    []string `json:"model"`
	Key      []string `json:"key"`
	Auth     []string `json:"auth"`
	Agent    []string `json:"agent"`
}

// EffectiveQuery echoes the query after defaults and the retention clamp.
type EffectiveQuery struct {
	From    string    `json:"from"` // YYYY-MM-DD
	To      string    `json:"to"`
	TZ      string    `json:"tz"`
	Bucket  Unit      `json:"bucket"`
	GroupBy Dimension `json:"group_by"`
	Clamped bool      `json:"clamped"` // From was moved forward to the retention horizon
}

// CatalogInfo describes where the prices came from.
type CatalogInfo struct {
	Source       string     `json:"source"` // "builtin" | "remote"
	SnapshotDate string     `json:"snapshot_date"` // date the embedded snapshot was generated
	FetchedAt    *time.Time `json:"fetched_at,omitempty"`
	NextFetchAt  *time.Time `json:"next_fetch_at,omitempty"`
	LastError    string     `json:"last_error,omitempty"`
	Remote       bool       `json:"remote"` // daily fetch enabled
}

// Report is the usage history response.
type Report struct {
	Enabled        bool              `json:"enabled"`
	Reason         Reason            `json:"reason,omitempty"`
	Query          EffectiveQuery    `json:"query"`
	AsOf           time.Time         `json:"as_of"` // the sync flush this report reflects
	RecordingSince *time.Time        `json:"recording_since,omitempty"` // oldest hour in the store
	RetentionDays  int               `json:"retention_days"`
	Buckets        []Bucket          `json:"buckets"`
	Groups         []GroupCell       `json:"groups"` // per-group totals over the range, sorted by cost desc then tokens desc
	Totals         Cell              `json:"totals"`
	UnpricedModels []string          `json:"unpriced_models"` // "provider/model" with tokens in range and no price
	Keys           map[string]string `json:"keys"`            // key hash -> masked configured key, for hashes present
	Options        Options           `json:"options"`
	Prices         CatalogInfo       `json:"prices"`
}

// ---------------------------------------------------------------------------
// Hourly: the internal read used by quotaforecast attribution (no pricing,
// no time zones, UTC hours, long-context tiers merged).
// ---------------------------------------------------------------------------

// HourlyQuery selects raw hourly rows.
type HourlyQuery struct {
	Since     time.Time // inclusive, truncated to the hour
	Providers []string  // empty = all
}

// HourlyRow is one rollup row with the tier dimension summed away.
type HourlyRow struct {
	Hour     int64 // unix seconds, UTC hour start
	Provider string
	AuthID   string
	Model    string
	KeyHash  string
	Agent    string
	Requests int64
	Failures int64
	Tokens   Tokens
}

// HourlyResult carries rows or the reason there are none.
type HourlyResult struct {
	Enabled bool
	Reason  Reason
	Rows    []HourlyRow
}
