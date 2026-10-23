package quotaforecast

import (
	"context"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory"
)

// Decision: ONE PATH. usagehistory is the only hourly usage aggregation in the
// process. quotaforecast stops being a usage plugin and reads the rows it
// regresses on from usagehistory at forecast time.
//
// Removed from quotaforecast:
//   - usage.go HandleUsage, uncachedTokens, forecastProviders' use for usage
//   - history.usage, addUsage, the usage part of compact/snapshot
//   - fileV1.Usage (the JSON decoder ignores the old "usage" key in existing
//     .quota-forecast.gz files; fileVersion stays 1)
//   - HashClientKey/AgentFamily (moved to usagehistory)
//   - the "quota-forecast" usage.RegisterNamedPlugin call
//
// Kept: UsageBucket (attribution's input type) and attribute() unchanged.
//
// Why one path is right here: the old bucket key (Hour, AuthID, Model, KeyHash,
// Agent) is a projection of the new key, and its Tokens (total - cacheRead) is
// a projection of the new Tokens. Retention 14-60 days is inside 365. The only
// thing quotaforecast loses is independence from the usage-history switch.
//
// When usage history is disabled (or home mode, or the DB is broken):
// attribution is &Attribution{Method: "none", Reason: "usage_history_<reason>"}
// and the panel's AttributionTable says "Turn on usage history to attribute
// quota burn". Forecasts, charts, alerts and hourly burn are unaffected: they
// come from quota windows, not usage.
//
// Transition cost: the first ~12 hours after deploy have fewer than nnlsMinRows
// burn rows, so attribution is "proportional" until history accumulates. The old
// 14 days of uncached-only buckets are not imported: they lack the token
// breakdown and would show up as odd rows on the Usage page.

// usageReader is the seam tests replace.
type usageReader interface {
	Hourly(ctx context.Context, q usagehistory.HourlyQuery) usagehistory.HourlyResult
}

var usageSource = func() usageReader { return usagehistory.Default() }

// attributionProviders are the providers whose quota windows are forecast.
var attributionProviders = []string{"claude", "codex", "xai", "devin"}

// loadUsage returns attribution buckets since `since`, or a reason when none.
//
//	res := usageSource().Hourly(ctx, HourlyQuery{Since: since, Providers: attributionProviders})
//	if !res.Enabled { return nil, "usage_history_" + string(res.Reason) }
//	for r in res.Rows where r.AuthID != "-":
//	    out = append(out, UsageBucket{Hour: r.Hour, AuthID: r.AuthID, Model: r.Model, KeyHash: r.KeyHash,
//	        Agent: r.Agent, Requests: r.Requests, Tokens: r.Tokens.Total() - r.Tokens.CacheRead})
func loadUsage(ctx context.Context, since time.Time) ([]UsageBucket, string) {
	panic("not implemented")
}

// Signature changes in service.go / forecast.go:
//
//	func (s *Service) Forecast(ctx context.Context, cfg *config.Config, auths []*coreauth.Auth, opts ForecastOptions) Response
//	  -> build(ctx, ...) calls loadUsage ONLY when detail (provider-scoped HTTP request);
//	     the tick's alert build (detail=false) never reads usage, so the forecast loop
//	     never waits on the usagehistory loop.
//	type Attribution struct { ...; Reason string `json:"reason,omitempty"` }   // new field
