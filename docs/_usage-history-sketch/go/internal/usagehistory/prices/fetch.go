package prices

import (
	"context"
	"net/http"
	"time"
)

// DefaultURL is LiteLLM's price file on GitHub (raw). Overridable in config.
const DefaultURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

const (
	// FetchTimeout bounds the whole daily fetch (connect + body). Named exception
	// to the AGENTS.md "no timeouts after connect" rule; add to its list:
	//   "the LiteLLM price fetch timeout in internal/usagehistory/prices/fetch.go".
	FetchTimeout = 30 * time.Second
	// RefreshInterval is the daily cadence, measured from the last 200/304.
	RefreshInterval = 24 * time.Hour
	// RetryInterval is the wait after a failed fetch or a rejected body.
	RetryInterval = time.Hour
	// maxBody caps the download (upstream file is ~1.5 MB today).
	maxBody = 32 << 20
)

// FetchResult is a completed fetch. NotModified means the cached catalog is still
// current (HTTP 304 for If-None-Match etag); Catalog is nil then.
type FetchResult struct {
	Catalog     *Catalog
	Body        []byte // Catalog.Encode(), what the DB caches
	ETag        string
	NotModified bool
	At          time.Time
}

// Fetch downloads url with If-None-Match: etag, trims the body and returns it.
// The caller supplies the client (the service builds it like the registry model
// updater does, so proxy-url is honoured the same way). Never retries itself.
//
// pseudocode:
//
//	ctx, cancel := context.WithTimeout(ctx, FetchTimeout); defer cancel()
//	req GET url; if etag != "" { req.Header.Set("If-None-Match", etag) }
//	resp := client.Do(req)              // defer close body, logging the close error
//	304 -> FetchResult{NotModified, ETag: etag, At: now}
//	!200 -> error "price fetch: HTTP <code>"
//	raw := io.ReadAll(io.LimitReader(resp.Body, maxBody+1)); len > maxBody -> error
//	cat := Trim(raw, SourceRemote, now)  // rejects < minEntries
//	body := cat.Encode()
//	return FetchResult{Catalog: cat, Body: body, ETag: resp.Header.Get("ETag"), At: now}
func Fetch(ctx context.Context, client *http.Client, url, etag string, now time.Time) (FetchResult, error) {
	return FetchResult{}, errNotImplemented
}

// Schedule is the pure fetch-due rule the run loop consults each tick.
type Schedule struct {
	LastOK    time.Time // last 200/304 (from price_cache.fetched_at on open)
	LastFail  time.Time
	InFlight  bool
	Disabled  bool // config disable-remote
}

// Due reports whether a fetch should start now.
//
//	!Disabled && !InFlight && now >= LastOK+RefreshInterval && now >= LastFail+RetryInterval
func (s Schedule) Due(now time.Time) bool { panic("not implemented") }

// Next is the earliest time Due can become true (for the prices endpoint), zero when disabled.
func (s Schedule) Next() time.Time { panic("not implemented") }
