package quotaforecast

import "time"

// Response is the payload of GET /v8/management/observability/quota/forecast.
// Instants inside Chart and HourlyBurn are unix seconds; all others are RFC 3339.
type Response struct {
	Enabled          bool       `json:"enabled"`
	Reason           string     `json:"reason,omitempty"`
	GeneratedAt      time.Time  `json:"generated_at"`
	RecordingSince   *time.Time `json:"recording_since"`
	QuotaPollEnabled bool       `json:"quota_poll_enabled"`
	RoutingStrategy  string     `json:"routing_strategy"`
	DrainModel       string     `json:"drain_model"`
	LookbackHours    int        `json:"lookback_hours"`
	RetentionDays    int        `json:"retention_days"`
	Pools            []Pool     `json:"pools"`
	Alerts           []Alert    `json:"alerts"`
}

// Pool is the forecast of one quota window class pooled across credentials.
type Pool struct {
	Class             string       `json:"class"`
	Provider          string       `json:"provider"`
	Window            string       `json:"window"`
	WindowSeconds     int64        `json:"window_seconds"`
	ModelScoped       bool         `json:"model_scoped"`
	AccountsTotal     int          `json:"accounts_total"`
	AccountsWithData  int          `json:"accounts_with_data"`
	RemainingAccounts float64      `json:"remaining_accounts"`
	NextResetAt       *time.Time   `json:"next_reset_at"`
	Burn              Burn         `json:"burn"`
	Runway            Runway       `json:"runway"`
	Pace              *Pace        `json:"pace"`
	Accounts          []Account    `json:"accounts,omitempty"`
	Chart             *Chart       `json:"chart,omitempty"`
	HourlyBurn        [][3]float64 `json:"hourly_burn,omitempty"`
	Attribution       *Attribution `json:"attribution,omitempty"`
}

// Burn is the pooled burn rate in accounts per hour with a low/high band taken
// from the 6, 12 and 24 hour rates.
type Burn struct {
	AccountsPerHour float64 `json:"accounts_per_hour"`
	Low             float64 `json:"low"`
	High            float64 `json:"high"`
	CoverageSeconds int64   `json:"coverage_seconds"`
	Sufficient      bool    `json:"sufficient"`
}

// Runway compares when the pool is projected to run out with when it recovers.
type Runway struct {
	Status                   string     `json:"status"`
	ExhaustsAt               *time.Time `json:"exhausts_at"`
	RecoversAt               *time.Time `json:"recovers_at"`
	ShortSeconds             int64      `json:"short_seconds"`
	ShortAccounts            float64    `json:"short_accounts"`
	SpareAtNextResetAccounts *float64   `json:"spare_at_next_reset_accounts"`
	TroughAccounts           float64    `json:"trough_accounts"`
	TroughAt                 *time.Time `json:"trough_at"`
	HorizonEnd               time.Time  `json:"horizon_end"`
}

// Pace compares the average used percentage with the share of the window elapsed.
type Pace struct {
	UsedPercent     float64 `json:"used_percent"`
	ExpectedPercent float64 `json:"expected_percent"`
	DeltaPP         float64 `json:"delta_pp"`
}

// Account is one credential's state within a pool.
type Account struct {
	AuthID               string     `json:"auth_id"`
	Name                 string     `json:"name"`
	UsedPercent          float64    `json:"used_percent"`
	RemainingPercent     float64    `json:"remaining_percent"`
	ResetAt              *time.Time `json:"reset_at"`
	ObservedAt           time.Time  `json:"observed_at"`
	ElapsedFraction      *float64   `json:"elapsed_fraction"`
	PaceDeltaPP          *float64   `json:"pace_delta_pp"`
	BurnPercentPerHour   float64    `json:"burn_percent_per_hour"`
	ProjectedUsedAtReset *float64   `json:"projected_used_at_reset"`
}

// Chart holds the recorded and projected pool totals in accounts.
type Chart struct {
	Start       int64           `json:"start"`
	Now         int64           `json:"now"`
	End         int64           `json:"end"`
	StepSeconds int64           `json:"step_seconds"`
	History     ChartHistory    `json:"history"`
	Projection  ChartProjection `json:"projection"`
	Resets      []ChartReset    `json:"resets"`
	Danger      []ChartDanger   `json:"danger"`
}

// ChartHistory is the recorded pool total and per-credential remaining percentage.
// Values are null before a credential's first observation.
type ChartHistory struct {
	Times     []int64        `json:"times"`
	Total     []*float64     `json:"total"`
	ByAccount []ChartAccount `json:"by_account"`
}

// ChartAccount is one credential's remaining percentage over ChartHistory.Times.
type ChartAccount struct {
	AuthID string     `json:"auth_id"`
	Values []*float64 `json:"values"`
}

// ChartProjection is the projected pool total at the central, low and high burn rates.
type ChartProjection struct {
	Times     []int64   `json:"times"`
	Central   []float64 `json:"central"`
	BestCase  []float64 `json:"optimistic"`
	WorstCase []float64 `json:"pessimistic"`
}

// ChartReset marks a projected credential reset.
type ChartReset struct {
	At     int64  `json:"at"`
	AuthID string `json:"auth_id"`
}

// ChartDanger marks an interval where the pool is projected (central rate) or
// possibly (high rate) empty.
type ChartDanger struct {
	From     int64  `json:"from"`
	To       int64  `json:"to"`
	Severity string `json:"severity"`
}

// Attribution splits the pool's recorded burn across models, client keys and agents.
type Attribution struct {
	Method  string    `json:"method"`
	R2      *float64  `json:"r2"`
	Rows    int       `json:"rows"`
	Models  []AttrRow `json:"models"`
	Clients []AttrRow `json:"clients"`
	Agents  []AttrRow `json:"agents"`
}

// AttrRow is the estimated burn of one model, client key or agent. PercentPerMTok
// is the used percentage of one credential per million uncached tokens.
type AttrRow struct {
	Model          string  `json:"model,omitempty"`
	KeyHash        string  `json:"key_hash,omitempty"`
	KeyMasked      string  `json:"key_masked,omitempty"`
	Agent          string  `json:"agent,omitempty"`
	Tokens         int64   `json:"tokens"`
	Requests       int64   `json:"requests"`
	BurnAccounts   float64 `json:"burn_accounts"`
	PercentPerMTok float64 `json:"percent_per_mtok"`
	Share          float64 `json:"share"`
}

// Alert is an active or recently resolved exhaustion alert.
type Alert struct {
	Class             string     `json:"class"`
	Provider          string     `json:"provider"`
	State             string     `json:"state"`
	FiredAt           *time.Time `json:"fired_at"`
	ExhaustsAt        *time.Time `json:"exhausts_at"`
	RecoversAt        *time.Time `json:"recovers_at"`
	ShortAccounts     float64    `json:"short_accounts"`
	RemainingAccounts float64    `json:"remaining_accounts"`
}
