package quotaforecast

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// historyFileName is deliberately not a .json file so the auth watcher and token
// stores never treat it as a credential.
const historyFileName = ".quota-forecast.gz"

const (
	// heartbeatInterval records an unchanged window at least this often so gaps in
	// the history are distinguishable from a steady value.
	heartbeatInterval = 15 * time.Minute
	// fullResolutionAge keeps every point newer than this; older points are thinned.
	fullResolutionAge = 48 * time.Hour
	// thinnedBucket is the resolution older points are thinned to.
	thinnedBucket = 15 * time.Minute
	// resetJitter tolerates reset timestamps that drift between observations, which
	// happens when a provider reports the reset relative to the response time.
	resetJitter = 10 * time.Minute
)

// history holds recorded quota series and alert state.
type history struct {
	mu     sync.Mutex
	series map[string]*Series
	alerts map[string]*alertRecord
	since  int64
	dirty  bool

	// fileMu serializes writes to the history file.
	fileMu sync.Mutex
}

func newHistory() *history {
	return &history{
		series: make(map[string]*Series),
		alerts: make(map[string]*alertRecord),
	}
}

func seriesKey(authID, class string) string { return authID + "|" + class }

// observe records w for authID when it carries new information. It reports
// whether a point was appended.
func (h *history) observe(authID string, w coreauth.QuotaWindow) bool {
	if authID == "" || w.Class == "" || w.ObservedAt.IsZero() {
		return false
	}
	point := Point{T: w.ObservedAt.Unix(), Used: math.Round(w.UsedPercent*100) / 100}
	if !w.ResetAt.IsZero() {
		point.Reset = w.ResetAt.Unix()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	key := seriesKey(authID, w.Class)
	s := h.series[key]
	if s == nil {
		s = &Series{AuthID: authID, Provider: w.Provider, Class: w.Class}
		h.series[key] = s
	}
	s.WindowSec = int64(w.Window / time.Second)
	if n := len(s.Points); n > 0 {
		last := s.Points[n-1]
		if point.T <= last.T {
			return false
		}
		changed := math.Abs(point.Used-last.Used) >= 0.01 || absInt64(point.Reset-last.Reset) > 60
		if !changed && point.T-last.T < int64(heartbeatInterval/time.Second) {
			return false
		}
	}
	s.Points = append(s.Points, point)
	if h.since == 0 || point.T < h.since {
		h.since = point.T
	}
	h.dirty = true
	return true
}

// compact drops data older than retention and thins points older than
// fullResolutionAge to one per thinnedBucket. Points that start a new reset cycle
// are always kept so burn calculations still see every reset.
func (h *history) compact(now time.Time, retention time.Duration) {
	cutoff := now.Add(-retention).Unix()
	thinBefore := now.Add(-fullResolutionAge).Unix()
	bucket := int64(thinnedBucket / time.Second)
	h.mu.Lock()
	defer h.mu.Unlock()
	for key, s := range h.series {
		kept := s.Points[:0]
		for i, p := range s.Points {
			if p.T < cutoff {
				continue
			}
			if p.T < thinBefore && i+1 < len(s.Points) {
				next := s.Points[i+1]
				sameBucket := next.T < thinBefore && next.T/bucket == p.T/bucket
				boundary := absInt64(next.Reset-p.Reset) > int64(resetJitter/time.Second) ||
					(len(kept) > 0 && absInt64(kept[len(kept)-1].Reset-p.Reset) > int64(resetJitter/time.Second))
				if sameBucket && !boundary {
					continue
				}
			}
			kept = append(kept, p)
		}
		if len(kept) != len(s.Points) {
			h.dirty = true
		}
		if len(kept) == 0 {
			delete(h.series, key)
			continue
		}
		s.Points = kept
	}
	if h.since != 0 && h.since < cutoff {
		h.since = cutoff
	}
}

// snapshot returns deep copies of the series (optionally filtered by provider)
// and alert records.
func (h *history) snapshot(provider string) ([]Series, []alertRecord, int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	series := make([]Series, 0, len(h.series))
	for _, s := range h.series {
		if provider != "" && s.Provider != provider {
			continue
		}
		copySeries := *s
		copySeries.Points = append([]Point(nil), s.Points...)
		series = append(series, copySeries)
	}
	sort.Slice(series, func(i, j int) bool {
		if series[i].Class != series[j].Class {
			return series[i].Class < series[j].Class
		}
		return series[i].AuthID < series[j].AuthID
	})
	alerts := make([]alertRecord, 0, len(h.alerts))
	for _, a := range h.alerts {
		alerts = append(alerts, *a)
	}
	sort.Slice(alerts, func(i, j int) bool { return alerts[i].Class < alerts[j].Class })
	return series, alerts, h.since
}

// load replaces the in-memory history with the contents of path. A missing file
// leaves the history empty.
func (h *history) load(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open quota history: %w", err)
	}
	defer func() {
		if errClose := f.Close(); errClose != nil {
			log.WithError(errClose).Debug("failed to close quota history file")
		}
	}()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("read quota history: %w", err)
	}
	var data fileV1
	if err = json.NewDecoder(gz).Decode(&data); err != nil {
		return fmt.Errorf("decode quota history: %w", err)
	}
	if data.Version != fileVersion {
		return fmt.Errorf("unsupported quota history version %d", data.Version)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.series = make(map[string]*Series, len(data.Series))
	for _, s := range data.Series {
		if s == nil || s.AuthID == "" || s.Class == "" {
			continue
		}
		sort.Slice(s.Points, func(i, j int) bool { return s.Points[i].T < s.Points[j].T })
		h.series[seriesKey(s.AuthID, s.Class)] = s
	}
	h.alerts = make(map[string]*alertRecord, len(data.Alerts))
	for _, a := range data.Alerts {
		if a != nil && a.Class != "" {
			h.alerts[a.Class] = a
		}
	}
	h.since = data.Since
	h.dirty = false
	return nil
}

// save writes the history to path atomically when it changed since the last save.
func (h *history) save(path string, now time.Time) error {
	h.mu.Lock()
	if !h.dirty {
		h.mu.Unlock()
		return nil
	}
	data := fileV1{Version: fileVersion, SavedAt: now.Unix(), Since: h.since}
	for _, s := range h.series {
		copySeries := *s
		copySeries.Points = append([]Point(nil), s.Points...)
		data.Series = append(data.Series, &copySeries)
	}
	for _, a := range h.alerts {
		copyAlert := *a
		data.Alerts = append(data.Alerts, &copyAlert)
	}
	h.dirty = false
	h.mu.Unlock()

	if err := writeHistoryFile(&h.fileMu, path, &data); err != nil {
		h.mu.Lock()
		h.dirty = true
		h.mu.Unlock()
		return err
	}
	return nil
}

func writeHistoryFile(mu *sync.Mutex, path string, data *fileV1) error {
	mu.Lock()
	defer mu.Unlock()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create quota history dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, historyFileName+".*.tmp")
	if err != nil {
		return fmt.Errorf("create quota history temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		if errRemove := os.Remove(tmpPath); errRemove != nil && !errors.Is(errRemove, os.ErrNotExist) {
			log.WithError(errRemove).Debug("failed to remove quota history temp file")
		}
	}
	gz := gzip.NewWriter(tmp)
	if err = json.NewEncoder(gz).Encode(data); err != nil {
		_ = gz.Close()
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("encode quota history: %w", err)
	}
	if err = gz.Close(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("compress quota history: %w", err)
	}
	if err = tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close quota history temp file: %w", err)
	}
	if err = os.Rename(tmpPath, path); err != nil {
		cleanup()
		return fmt.Errorf("replace quota history: %w", err)
	}
	return nil
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
