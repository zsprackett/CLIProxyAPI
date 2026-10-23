package config

// --- internal/config/config_types.go (new types, next to RequestHistoryConfig) ---

// UsageHistoryConfig controls the long-term hourly usage rollups and the price
// catalog behind GET /v8/management/observability/usage/history.
type UsageHistoryConfig struct {
	// Enabled turns on recording into <auth-dir>/.usage-history.db. Default false.
	// Turning it off stops recording and closes the file; history is kept on disk.
	Enabled bool `yaml:"enabled" json:"enabled"`

	// RetentionDays keeps hourly rollups this long. Default 365, min 1, max 3650.
	// Raising it keeps more from now on; lowering it deletes older rows at the next
	// cleanup (within a minute of the reload, then daily).
	RetentionDays int `yaml:"retention-days,omitempty" json:"retention-days,omitempty"`

	// Prices configures the price catalog used to estimate cost at read time.
	Prices UsagePricesConfig `yaml:"prices,omitempty" json:"prices,omitempty"`
}

// UsagePricesConfig configures the price catalog.
type UsagePricesConfig struct {
	// DisableRemote stops the daily LiteLLM fetch; the built-in snapshot (and the
	// last cached fetch, if any) stays in use.
	DisableRemote bool `yaml:"disable-remote,omitempty" json:"disable-remote,omitempty"`

	// RemoteURL replaces the LiteLLM model_prices_and_context_window.json URL
	// (e.g. a mirror). Empty = the GitHub raw URL.
	RemoteURL string `yaml:"remote-url,omitempty" json:"remote-url,omitempty"`

	// Models overrides or adds prices per model. First match by provider+model,
	// then model alone. Rates are USD per million tokens.
	Models []ModelPriceOverride `yaml:"models,omitempty" json:"models,omitempty"`
}

// ModelPriceOverride prices one upstream model id (Record.Model).
type ModelPriceOverride struct {
	Model    string `yaml:"model" json:"model"`
	Provider string `yaml:"provider,omitempty" json:"provider,omitempty"`
	// SameAs prices Model like this LiteLLM key, e.g. an antigravity id like
	// "claude-opus-4-6-thinking" same-as "claude-opus-4-6". Explicit rates below
	// still win.
	SameAs       string   `yaml:"same-as,omitempty" json:"same-as,omitempty"`
	Input        *float64 `yaml:"input,omitempty" json:"input,omitempty"`
	Output       *float64 `yaml:"output,omitempty" json:"output,omitempty"`
	Reasoning    *float64 `yaml:"reasoning,omitempty" json:"reasoning,omitempty"`
	CacheRead    *float64 `yaml:"cache-read,omitempty" json:"cache-read,omitempty"`
	CacheWrite5m *float64 `yaml:"cache-write-5m,omitempty" json:"cache-write-5m,omitempty"`
	CacheWrite1h *float64 `yaml:"cache-write-1h,omitempty" json:"cache-write-1h,omitempty"`
}

// --- internal/config/config.go, on Config next to RequestHistory ---
//
//	// UsageHistory keeps a year of hourly usage rollups with API-price cost estimates.
//	UsageHistory UsageHistoryConfig `yaml:"usage-history" json:"usage-history"`
//
// --- internal/config/config_v8.go buildV8Paths prefixes, next to request-history ---
//
//	{"usage-history", "observability.usage.usage-history"},
//
// --- internal/config/config_diff.go ---
//
//	usage-history.enabled: false -> true
//	usage-history.retention-days: 365 -> 730
//	usage-history.prices.disable-remote: false -> true
//	usage-history.prices.remote-url: changed
//	usage-history.prices.models: 2 -> 3 overrides
//
// Hot reload (pull model, no watcher callback): the usagehistory run loop reads
// manager.RuntimeConfig() every tick (1 min) and on every HTTP report/prices
// request (via sync), so a reload takes effect at the next request or minute:
//   enabled false->true   open file, start recording
//   enabled true->false   stop recording, flush, close (file kept)
//   home.enabled          same as disabled (reason home_mode)
//   auth-dir change       flush to the old file, close it, open <new-auth-dir>/.usage-history.db
//                         (no copy: the old file stays where it was)
//   retention-days        cleanup runs on the next tick with the new cutoff
//   prices.*              price table rebuilt; remote-url or disable-remote change resets the fetch schedule
