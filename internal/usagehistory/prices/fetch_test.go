package prices

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// litellmBody builds a LiteLLM-shaped file with n priced chat models.
func litellmBody(n int) string {
	var b strings.Builder
	b.WriteString(`{"sample_spec":{"mode":"chat"}`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `,"model-%d":{"mode":"chat","input_cost_per_token":1e-06,"output_cost_per_token":2e-06}`, i)
	}
	b.WriteString(`}`)
	return b.String()
}

// priceServer answers every request with handler and records the If-None-Match values it saw.
type priceServer struct {
	*httptest.Server
	mu          sync.Mutex
	ifNoneMatch []string
}

func newPriceServer(t *testing.T, handler http.HandlerFunc) *priceServer {
	t.Helper()
	ps := &priceServer{}
	ps.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ps.mu.Lock()
		ps.ifNoneMatch = append(ps.ifNoneMatch, r.Header.Get("If-None-Match"))
		ps.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(ps.Close)
	return ps
}

func TestFetch(t *testing.T) {
	tests := []struct {
		name        string
		etag        string
		handler     http.HandlerFunc
		wantErr     string
		wantNotMod  bool
		wantETag    string
		wantEntries int
		wantSent    string
	}{
		{
			name: "200 returns a trimmed catalog and the new etag",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", `"v2"`)
				fmt.Fprint(w, litellmBody(250))
			},
			wantETag: `"v2"`, wantEntries: 250,
		},
		{
			name: "200 sends If-None-Match with the cached etag",
			etag: `"v1"`,
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", `"v2"`)
				fmt.Fprint(w, litellmBody(250))
			},
			wantETag: `"v2"`, wantEntries: 250, wantSent: `"v1"`,
		},
		{
			name: "304 keeps the cached catalog",
			etag: `"v1"`,
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotModified)
			},
			wantNotMod: true, wantETag: `"v1"`, wantSent: `"v1"`,
		},
		{
			name: "500 is an error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "boom", http.StatusInternalServerError)
			},
			wantErr: "HTTP 500",
		},
		{
			name: "404 is an error",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.NotFound(w, r)
			},
			wantErr: "HTTP 404",
		},
		{
			name: "short catalog is rejected",
			handler: func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, litellmBody(minEntries-2))
			},
			wantErr: "fewer than",
		},
		{
			name: "html page is rejected",
			handler: func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, "<html>captive portal</html>")
			},
			wantErr: "parse",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newPriceServer(t, tt.handler)
			got, err := Fetch(context.Background(), srv.Client(), srv.URL, tt.etag, testNow)
			if sent := srv.ifNoneMatch; len(sent) != 1 || sent[0] != tt.wantSent {
				t.Errorf("If-None-Match sent = %q, want one request with %q", sent, tt.wantSent)
			}
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				if got.Catalog != nil || got.Body != nil {
					t.Errorf("a failed fetch returned a catalog: %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Fetch: %v", err)
			}
			if got.NotModified != tt.wantNotMod || got.ETag != tt.wantETag || !got.At.Equal(testNow) {
				t.Errorf("NotModified=%v ETag=%q At=%v", got.NotModified, got.ETag, got.At)
			}
			if tt.wantNotMod {
				if got.Catalog != nil || got.Body != nil {
					t.Errorf("304 returned a catalog: %+v", got)
				}
				return
			}
			if got.Catalog == nil || got.Catalog.Source != SourceRemote || len(got.Catalog.Entries) != tt.wantEntries {
				t.Fatalf("catalog = %+v", got.Catalog)
			}
			cached, err := Decode(got.Body)
			if err != nil || len(cached.Entries) != tt.wantEntries || cached.Source != SourceRemote {
				t.Errorf("Body does not decode to the same catalog (err=%v)", err)
			}
		})
	}
}

func TestFetchBodyCap(t *testing.T) {
	body := litellmBody(250)
	tests := []struct {
		name    string
		maxBody int64
		wantErr string
	}{
		{"body exactly at the cap is read", int64(len(body)), ""},
		{"body one byte over the cap is rejected", int64(len(body)) - 1, "too large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newPriceServer(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
			_, err := fetch(context.Background(), srv.Client(), srv.URL, "", testNow, fetchLimits{timeout: time.Minute, maxBody: tt.maxBody})
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Fetch: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestFetchStopsWithTheContext(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, srv *priceServer, started <-chan struct{}) error
		want error
	}{
		{
			name: "deadline already passed",
			run: func(t *testing.T, srv *priceServer, _ <-chan struct{}) error {
				_, err := fetch(context.Background(), srv.Client(), srv.URL, "", testNow, fetchLimits{timeout: time.Nanosecond, maxBody: maxBody})
				return err
			},
			want: context.DeadlineExceeded,
		},
		{
			name: "caller cancels while the server hangs",
			run: func(t *testing.T, srv *priceServer, started <-chan struct{}) error {
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan error, 1)
				go func() {
					_, err := Fetch(ctx, srv.Client(), srv.URL, "", testNow)
					done <- err
				}()
				<-started
				cancel()
				return <-done
			},
			want: context.Canceled,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			started := make(chan struct{}, 1)
			srv := newPriceServer(t, func(w http.ResponseWriter, r *http.Request) {
				select {
				case started <- struct{}{}:
				default:
				}
				<-r.Context().Done()
			})
			if err := tt.run(t, srv, started); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestNewClientTimeout(t *testing.T) {
	if got := NewClient().Timeout; got != FetchTimeout || FetchTimeout != 30*time.Second {
		t.Errorf("client timeout = %v, FetchTimeout = %v, want 30s", got, FetchTimeout)
	}
}

func TestScheduleDue(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	tests := []struct {
		name string
		s    Schedule
		want bool
	}{
		{"never fetched", Schedule{}, true},
		{"disabled", Schedule{Disabled: true}, false},
		{"in flight", Schedule{InFlight: true}, false},
		{"fetched 23h59m ago", Schedule{LastOK: ago(24*time.Hour - time.Minute)}, false},
		{"fetched exactly 24h ago", Schedule{LastOK: ago(24 * time.Hour)}, true},
		{"fetched 30h ago", Schedule{LastOK: ago(30 * time.Hour)}, true},
		{"failed 59m ago, old success", Schedule{LastOK: ago(48 * time.Hour), LastFail: ago(59 * time.Minute)}, false},
		{"failed exactly 1h ago, old success", Schedule{LastOK: ago(48 * time.Hour), LastFail: ago(time.Hour)}, true},
		{"failed 2h ago, never succeeded", Schedule{LastFail: ago(2 * time.Hour)}, true},
		{"failed long ago, success 1h ago", Schedule{LastOK: ago(time.Hour), LastFail: ago(30 * time.Hour)}, false},
		{"disabled beats an old success", Schedule{Disabled: true, LastOK: ago(72 * time.Hour)}, false},
		{"in flight beats an old success", Schedule{InFlight: true, LastOK: ago(72 * time.Hour)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.Due(now); got != tt.want {
				t.Errorf("Due = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestScheduleNext(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		s    Schedule
		want time.Time
	}{
		{"disabled has no next time", Schedule{Disabled: true, LastOK: now}, time.Time{}},
		{"a day after the last success", Schedule{LastOK: now}, now.Add(24 * time.Hour)},
		{"an hour after a failure when the success is old", Schedule{LastOK: now.Add(-48 * time.Hour), LastFail: now}, now.Add(time.Hour)},
		{"the success still wins when the failure is older", Schedule{LastOK: now, LastFail: now.Add(-time.Hour)}, now.Add(24 * time.Hour)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.s.Next()
			if !got.Equal(tt.want) {
				t.Fatalf("Next = %v, want %v", got, tt.want)
			}
			if tt.s.Disabled {
				return
			}
			if !tt.s.Due(got) || tt.s.Due(got.Add(-time.Nanosecond)) {
				t.Errorf("Next %v is not the first instant Due turns true", got)
			}
		})
	}
	if next := (Schedule{}).Next(); next.IsZero() || next.After(now) {
		t.Errorf("never fetched: Next = %v, want a past time that is not the zero value", next)
	}
}
