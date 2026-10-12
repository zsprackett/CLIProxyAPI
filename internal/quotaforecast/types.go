// Package quotaforecast records observed OAuth quota history and derives pooled
// trend, burn-rate, runway, pace, token attribution and exhaustion alerts from it.
package quotaforecast

// Point is one observation of a quota window. T and Reset are unix seconds and
// Used is the used percentage (0-100). Reset is 0 when the reset time is unknown.
type Point struct {
	T     int64   `json:"t"`
	Used  float64 `json:"u"`
	Reset int64   `json:"r,omitempty"`
}

// Series is the recorded history of one quota window class for one credential.
type Series struct {
	AuthID    string  `json:"auth_id"`
	Provider  string  `json:"provider"`
	Class     string  `json:"class"`
	WindowSec int64   `json:"window_sec"`
	Points    []Point `json:"points"`
}

// usageKey identifies one hourly token usage bucket.
type usageKey struct {
	Hour    int64
	AuthID  string
	Model   string
	KeyHash string
	Agent   string
}

// UsageBucket aggregates the uncached tokens one client sent to one model through
// one credential during one hour. KeyHash is a truncated SHA-256 of the client API
// key; the raw key is never stored.
type UsageBucket struct {
	Hour     int64  `json:"hour"`
	AuthID   string `json:"auth_id"`
	Model    string `json:"model"`
	KeyHash  string `json:"key_hash"`
	Agent    string `json:"agent"`
	Requests int64  `json:"requests"`
	Tokens   int64  `json:"tokens"`
}

func (b UsageBucket) key() usageKey {
	return usageKey{Hour: b.Hour, AuthID: b.AuthID, Model: b.Model, KeyHash: b.KeyHash, Agent: b.Agent}
}

// Alert states.
const (
	alertPending  = "pending"
	alertFiring   = "firing"
	alertResolved = "resolved"
)

// alertRecord tracks the exhaustion alert of one pooled quota window class.
type alertRecord struct {
	Class             string  `json:"class"`
	Provider          string  `json:"provider"`
	State             string  `json:"state"`
	Consecutive       int     `json:"consecutive,omitempty"`
	Clear             int     `json:"clear,omitempty"`
	FiredAt           int64   `json:"fired_at,omitempty"`
	ExhaustsAt        int64   `json:"exhausts_at,omitempty"`
	RecoversAt        int64   `json:"recovers_at,omitempty"`
	RearmAt           int64   `json:"rearm_at,omitempty"`
	ShortAccounts     float64 `json:"short_accounts,omitempty"`
	RemainingAccounts float64 `json:"remaining_accounts,omitempty"`
}

// fileV1 is the on-disk history format.
type fileV1 struct {
	Version int            `json:"version"`
	SavedAt int64          `json:"saved_at"`
	Since   int64          `json:"since,omitempty"`
	Series  []*Series      `json:"series"`
	Usage   []UsageBucket  `json:"usage"`
	Alerts  []*alertRecord `json:"alerts,omitempty"`
}

const fileVersion = 1
