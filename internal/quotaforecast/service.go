package quotaforecast

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	tickInterval = time.Minute
	// flushEvery and compactEvery are counted in ticks.
	flushEvery   = 5
	compactEvery = 60

	defaultRetentionDays = 14
	maxRetentionDays     = 60
	defaultLookbackHours = 12
)

// Service records quota history from the auth manager and serves forecasts.
type Service struct {
	hist    *history
	enabled atomic.Bool
	now     func() time.Time
	notify  func(cfg *config.Config, rec alertRecord, now time.Time)

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	path   string
	loaded string
}

var defaultService = newService()

// Default returns the process-wide quota forecast service.
func Default() *Service { return defaultService }

func newService() *Service {
	return &Service{
		hist: newHistory(),
		now:  time.Now,
		notify: func(cfg *config.Config, rec alertRecord, now time.Time) {
			go notifyAlert(cfg, rec, now)
		},
	}
}

// Start begins recording quota history from manager until ctx is cancelled or
// Stop is called. It is a no-op when already running.
func (s *Service) Start(ctx context.Context, manager *coreauth.Manager) {
	if s == nil || manager == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.run(runCtx, manager, s.done)
}

// Stop ends recording and writes any unsaved history.
func (s *Service) Stop(ctx context.Context) {
	if s == nil {
		return
	}
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.cancel, s.done = nil, nil
	s.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func (s *Service) run(ctx context.Context, manager *coreauth.Manager, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(tickInterval)
	defer ticker.Stop()
	tick := 0
	for {
		s.tick(ctx, manager, tick)
		tick++
		select {
		case <-ctx.Done():
			s.flush()
			return
		case <-ticker.C:
		}
	}
}

// tick samples the manager's credentials, evaluates alerts and periodically
// compacts and saves the history.
func (s *Service) tick(ctx context.Context, manager *coreauth.Manager, tick int) {
	cfg := manager.RuntimeConfig()
	if !s.refreshEnabled(cfg) {
		return
	}
	now := s.now()
	auths := manager.List()
	s.sample(auths)
	if tick%compactEvery == 0 {
		s.hist.compact(now, retention(cfg))
	}
	resp := s.build(ctx, cfg, auths, "", defaultLookbackHours, 0, false)
	lead := time.Duration(cfg.QuotaForecast.AlertLeadHours) * time.Hour
	for _, rec := range s.hist.evaluateAlerts(resp.Pools, now, lead) {
		s.notify(cfg, rec, now)
	}
	if tick%flushEvery == 0 {
		s.flush()
	}
}

// refreshEnabled updates the enabled flag and history path from cfg, loading the
// history file the first time a path is seen.
func (s *Service) refreshEnabled(cfg *config.Config) bool {
	enabled := cfg != nil && cfg.QuotaForecast.Enabled && !cfg.Home.Enabled
	if !enabled {
		s.enabled.Store(false)
		return false
	}
	dir, err := util.ResolveAuthDir(cfg.AuthDir)
	if err != nil {
		log.WithError(err).Warn("quota forecast: cannot resolve auth dir")
		s.enabled.Store(false)
		return false
	}
	path := filepath.Join(dir, historyFileName)
	s.mu.Lock()
	s.path = path
	needLoad := s.loaded != path
	s.loaded = path
	s.mu.Unlock()
	if needLoad {
		if errLoad := s.hist.load(path); errLoad != nil {
			log.WithError(errLoad).Warn("quota forecast: ignoring unreadable history file")
		}
	}
	s.enabled.Store(true)
	return true
}

func (s *Service) flush() {
	s.mu.Lock()
	path := s.path
	s.mu.Unlock()
	if path == "" || !s.enabled.Load() {
		return
	}
	if err := s.hist.save(path, s.now()); err != nil {
		log.WithError(err).Warn("quota forecast: failed to save history")
	}
}

// sample records the current quota windows of every enabled credential.
func (s *Service) sample(auths []*coreauth.Auth) {
	for _, auth := range auths {
		if !forecastAuth(auth) {
			continue
		}
		for _, w := range coreauth.QuotaWindowsForAuth(auth) {
			s.hist.observe(auth.ID, w)
		}
	}
}

// forecastProviders lists the providers whose quota windows are recorded.
var forecastProviders = map[string]struct{}{"claude": {}, "codex": {}, "xai": {}, "devin": {}}

func forecastAuth(auth *coreauth.Auth) bool {
	if auth == nil || auth.Disabled || auth.Status == coreauth.StatusDisabled {
		return false
	}
	_, ok := forecastProviders[strings.ToLower(strings.TrimSpace(auth.Provider))]
	return ok
}

// ForecastOptions selects what Forecast returns.
type ForecastOptions struct {
	// Provider limits the response to one provider and adds charts, per-account
	// detail, hourly burn and attribution. Empty returns summaries of every pool.
	Provider      string
	LookbackHours int
	HistoryHours  int
}

// Forecast samples auths and returns the forecast of every pooled quota window.
// A provider-scoped forecast reads usage history through ctx for attribution.
func (s *Service) Forecast(ctx context.Context, cfg *config.Config, auths []*coreauth.Auth, opts ForecastOptions) Response {
	now := s.now().UTC()
	if cfg == nil || !cfg.QuotaForecast.Enabled {
		return Response{Enabled: false, Reason: "disabled", GeneratedAt: now, Pools: []Pool{}, Alerts: []Alert{}}
	}
	if cfg.Home.Enabled {
		return Response{Enabled: false, Reason: "home_mode", GeneratedAt: now, Pools: []Pool{}, Alerts: []Alert{}}
	}
	if !s.refreshEnabled(cfg) {
		return Response{Enabled: false, Reason: "unavailable", GeneratedAt: now, Pools: []Pool{}, Alerts: []Alert{}}
	}
	s.sample(auths)
	provider := strings.ToLower(strings.TrimSpace(opts.Provider))
	return s.build(ctx, cfg, auths, provider, opts.LookbackHours, opts.HistoryHours, provider != "")
}

// build reads usage history only when detail is set, so the alert tick never
// waits on it.
func (s *Service) build(ctx context.Context, cfg *config.Config, auths []*coreauth.Auth, provider string, lookbackHours, historyHours int, detail bool) Response {
	now := s.now().UTC()
	lookbackHours = clampInt(lookbackHours, defaultLookbackHours, 6, 24)
	retentionDays := clampInt(cfg.QuotaForecast.RetentionDays, defaultRetentionDays, 1, maxRetentionDays)
	strategy := strings.TrimSpace(cfg.Routing.Strategy)
	if strategy == "" {
		strategy = "round-robin"
	}
	resp := Response{
		Enabled:          true,
		GeneratedAt:      now,
		QuotaPollEnabled: cfg.QuotaPollIntervalSeconds > 0,
		RoutingStrategy:  strategy,
		DrainModel:       drainModelForStrategy(strategy),
		LookbackHours:    lookbackHours,
		RetentionDays:    retentionDays,
		Pools:            []Pool{},
	}

	series, alerts, since := s.hist.snapshot(provider)
	resp.RecordingSince = unixPtr(since)
	resp.Alerts = alertsForResponse(alerts)
	if provider != "" {
		filtered := resp.Alerts[:0]
		for _, a := range resp.Alerts {
			if a.Provider == provider {
				filtered = append(filtered, a)
			}
		}
		resp.Alerts = filtered
	}

	names := make(map[string]string)
	providerCounts := make(map[string]int)
	for _, auth := range auths {
		if !forecastAuth(auth) {
			continue
		}
		name := strings.TrimSpace(auth.FileName)
		if name == "" {
			name = auth.ID
		}
		names[auth.ID] = filepath.Base(name)
		providerCounts[strings.ToLower(strings.TrimSpace(auth.Provider))]++
	}

	byClass := make(map[string][]member)
	classProvider := make(map[string]string)
	nowUnix := now.Unix()
	for _, sr := range series {
		if _, ok := names[sr.AuthID]; !ok || len(sr.Points) == 0 {
			continue
		}
		last := sr.Points[len(sr.Points)-1]
		if last.Reset == 0 && sr.WindowSec > 0 && nowUnix-last.T > sr.WindowSec {
			// Without a reset time a stale observation says nothing about today.
			continue
		}
		byClass[sr.Class] = append(byClass[sr.Class], member{AuthID: sr.AuthID, WindowSec: sr.WindowSec, Points: sr.Points})
		classProvider[sr.Class] = sr.Provider
	}
	classes := make([]string, 0, len(byClass))
	for class := range byClass {
		classes = append(classes, class)
	}
	sort.Strings(classes)

	retentionSec := int64(retentionDays) * 86400
	var maskedKeys map[string]string
	var usage []UsageBucket
	var usageReason string
	if detail {
		maskedKeys = make(map[string]string, len(cfg.APIKeys))
		for _, key := range cfg.APIKeys {
			maskedKeys[usagehistory.HashClientKey(key)] = util.HideAPIKey(strings.TrimSpace(key))
		}
		usage, usageReason = loadUsage(ctx, time.Unix(nowUnix-retentionSec, 0).UTC())
	}
	for _, class := range classes {
		members := byClass[class]
		windowSec := int64(0)
		for _, m := range members {
			windowSec = max(windowSec, m.WindowSec)
		}
		history := int64(historyHours) * 3600
		if history <= 0 {
			history = max(int64(24*3600), windowSec)
		}
		history = min(history, retentionSec)
		opts := poolOptions{
			now:          nowUnix,
			lookback:     int64(lookbackHours) * 3600,
			history:      history,
			drain:        resp.DrainModel,
			detail:       detail,
			usage:        usage,
			usageReason:  usageReason,
			maskedKeys:   maskedKeys,
			accountNames: names,
		}
		resp.Pools = append(resp.Pools, computePool(class, classProvider[class], members, providerCounts[classProvider[class]], opts))
	}
	return resp
}

func retention(cfg *config.Config) time.Duration {
	return time.Duration(clampInt(cfg.QuotaForecast.RetentionDays, defaultRetentionDays, 1, maxRetentionDays)) * 24 * time.Hour
}

func clampInt(v, fallback, lo, hi int) int {
	if v <= 0 {
		return fallback
	}
	return max(lo, min(hi, v))
}
