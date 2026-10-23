package quotaforecast

import (
	"context"
	"maps"
	"slices"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory"
)

// usageReader is the seam tests replace.
type usageReader interface {
	Hourly(ctx context.Context, q usagehistory.HourlyQuery) usagehistory.HourlyResult
}

var usageSource = func() usageReader { return usagehistory.Default() }

// attributionProviders are the providers whose quota windows are forecast.
var attributionProviders = slices.Sorted(maps.Keys(forecastProviders))

// loadUsage returns the attribution buckets since `since`, or the reason there are
// none. Tokens exclude cache reads, which cost a small fraction of fresh tokens
// against a subscription quota. Rows without a credential cannot be attributed.
func loadUsage(ctx context.Context, since time.Time) ([]UsageBucket, string) {
	res := usageSource().Hourly(ctx, usagehistory.HourlyQuery{Since: since, Providers: attributionProviders})
	if !res.Enabled {
		return nil, "usage_history_" + string(res.Reason)
	}
	out := make([]UsageBucket, 0, len(res.Rows))
	for _, r := range res.Rows {
		if r.AuthID == "-" {
			continue
		}
		out = append(out, UsageBucket{
			Hour:     r.Hour,
			AuthID:   r.AuthID,
			Model:    r.Model,
			KeyHash:  r.KeyHash,
			Agent:    r.Agent,
			Requests: r.Requests,
			Tokens:   r.Tokens.Total() - r.Tokens.CacheRead,
		})
	}
	return out, ""
}
