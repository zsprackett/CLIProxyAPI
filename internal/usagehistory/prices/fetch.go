package prices

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
)

// DefaultURL is LiteLLM's price file on GitHub (raw). Overridable in config.
const DefaultURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

const (
	// FetchTimeout bounds the whole daily fetch (connect + body). It is a named
	// exception to the AGENTS.md "no timeouts after connect" rule: the exception
	// list there names this file.
	FetchTimeout = 30 * time.Second
	// RefreshInterval is the daily cadence, measured from the last 200/304.
	RefreshInterval = 24 * time.Hour
	// RetryInterval is the wait after a failed fetch or a rejected body.
	RetryInterval = time.Hour
	// maxBody caps the download (upstream file is ~3 MB today).
	maxBody = 32 << 20
)

// fetchLimits are the two bounds of one fetch. Fetch uses the constants above;
// tests pass smaller values to reach the limits without a 30s wait or a 32 MB body.
type fetchLimits struct {
	timeout time.Duration
	maxBody int64
}

// NewClient returns the HTTP client for Fetch: a plain client with FetchTimeout,
// built like the registry model updater's client (no proxy-url).
func NewClient() *http.Client { return &http.Client{Timeout: FetchTimeout} }

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
// A nil client means NewClient(). Fetch never retries itself.
func Fetch(ctx context.Context, client *http.Client, url, etag string, now time.Time) (FetchResult, error) {
	return fetch(ctx, client, url, etag, now, fetchLimits{timeout: FetchTimeout, maxBody: maxBody})
}

func fetch(ctx context.Context, client *http.Client, url, etag string, now time.Time, limits fetchLimits) (FetchResult, error) {
	if client == nil {
		client = NewClient()
	}
	ctx, cancel := context.WithTimeout(ctx, limits.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return FetchResult{}, fmt.Errorf("price fetch: build request: %w", err)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := client.Do(req)
	if err != nil {
		return FetchResult{}, fmt.Errorf("price fetch: %w", err)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Debugf("price fetch: close response body: %v", errClose)
		}
	}()

	switch resp.StatusCode {
	case http.StatusNotModified:
		return FetchResult{ETag: etag, NotModified: true, At: now}, nil
	case http.StatusOK:
	default:
		return FetchResult{}, fmt.Errorf("price fetch: HTTP %d", resp.StatusCode)
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, limits.maxBody+1))
	if err != nil {
		return FetchResult{}, fmt.Errorf("price fetch: read body: %w", err)
	}
	if int64(len(raw)) > limits.maxBody {
		return FetchResult{}, fmt.Errorf("price fetch: body too large (over %d bytes)", limits.maxBody)
	}
	catalog, err := Trim(raw, SourceRemote, now)
	if err != nil {
		return FetchResult{}, fmt.Errorf("price fetch: %w", err)
	}
	body, err := catalog.Encode()
	if err != nil {
		return FetchResult{}, fmt.Errorf("price fetch: %w", err)
	}
	return FetchResult{Catalog: catalog, Body: body, ETag: resp.Header.Get("ETag"), At: now}, nil
}

// Schedule is the pure fetch-due rule the run loop consults each tick.
type Schedule struct {
	LastOK   time.Time // last 200/304 (from price_cache.fetched_at on open)
	LastFail time.Time
	InFlight bool
	Disabled bool // config disable-remote
}

// Due reports whether a fetch should start now.
//
//	!Disabled && !InFlight && now >= LastOK+RefreshInterval && now >= LastFail+RetryInterval
func (s Schedule) Due(now time.Time) bool {
	return !s.Disabled && !s.InFlight && !now.Before(s.Next())
}

// Next is the earliest time Due can become true (for the prices endpoint), zero
// when disabled. It ignores InFlight. A schedule that never fetched returns a time in
// year 1, which is in the past: due now.
func (s Schedule) Next() time.Time {
	if s.Disabled {
		return time.Time{}
	}
	next := s.LastOK.Add(RefreshInterval)
	if retry := s.LastFail.Add(RetryInterval); retry.After(next) {
		next = retry
	}
	return next
}
