package auth

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	log "github.com/sirupsen/logrus"
)

// QuotaPoller is implemented by executors that can fetch a credential's current
// quota without consuming it. The returned headers use the same names as the
// passive response-header observation so both feed one snapshot. A nil header
// with a nil error means the credential has nothing to poll (e.g. API keys).
type QuotaPoller interface {
	PollQuota(ctx context.Context, auth *Auth) (http.Header, error)
}

const (
	// quotaPollCheckInterval is how often the poll loop re-reads its configured
	// interval and looks for due credentials, so hot-reloaded config applies promptly.
	quotaPollCheckInterval = 30 * time.Second
	// quotaPollMaxConcurrency bounds simultaneous upstream usage requests.
	quotaPollMaxConcurrency = 4
)

type quotaPollTarget struct {
	auth   *Auth
	poller QuotaPoller
}

// quotaPollInterval returns the configured quota poll interval, or 0 when disabled.
func (m *Manager) quotaPollInterval() time.Duration {
	if m == nil {
		return 0
	}
	cfg, ok := m.runtimeConfig.Load().(*internalconfig.Config)
	if !ok || cfg == nil || cfg.QuotaPollIntervalSeconds <= 0 {
		return 0
	}
	return time.Duration(cfg.QuotaPollIntervalSeconds) * time.Second
}

// quotaPollDue reports whether a credential last polled at lastPolled should be polled now.
func quotaPollDue(now, lastPolled time.Time, interval time.Duration) bool {
	return interval > 0 && (lastPolled.IsZero() || !now.Before(lastPolled.Add(interval)))
}

// quotaPollTargets snapshots the enabled credentials whose executor supports quota polling.
func (m *Manager) quotaPollTargets() []quotaPollTarget {
	m.mu.RLock()
	defer m.mu.RUnlock()
	targets := make([]quotaPollTarget, 0, len(m.auths))
	for _, auth := range m.auths {
		if auth == nil || auth.Disabled || auth.Status == StatusDisabled || !ProviderSupportsQuotaObservation(auth.Provider) {
			continue
		}
		exec, ok := m.executorLocked(executorKeyFromAuth(auth))
		if !ok || exec == nil {
			continue
		}
		poller, ok := exec.(QuotaPoller)
		if !ok {
			continue
		}
		targets = append(targets, quotaPollTarget{auth: auth.Clone(), poller: poller})
	}
	return targets
}

// runQuotaPoll polls each credential's quota at the configured
// quota-poll-interval-seconds until ctx is cancelled. Each credential is polled
// independently so one slow upstream never delays the others.
func (m *Manager) runQuotaPoll(ctx context.Context) {
	ticker := time.NewTicker(quotaPollCheckInterval)
	defer ticker.Stop()
	lastPolled := make(map[string]time.Time)
	var inFlight sync.Map
	slots := make(chan struct{}, quotaPollMaxConcurrency)
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			interval := m.quotaPollInterval()
			if interval <= 0 {
				continue
			}
			targets := m.quotaPollTargets()
			current := make(map[string]struct{}, len(targets))
			for _, target := range targets {
				id := target.auth.ID
				current[id] = struct{}{}
				if !quotaPollDue(now, lastPolled[id], interval) {
					continue
				}
				if _, busy := inFlight.LoadOrStore(id, struct{}{}); busy {
					continue
				}
				select {
				case slots <- struct{}{}:
				default:
					// All slots busy; this credential is retried on the next tick.
					inFlight.Delete(id)
					continue
				}
				lastPolled[id] = now
				go func(target quotaPollTarget) {
					defer func() {
						<-slots
						inFlight.Delete(target.auth.ID)
					}()
					m.pollQuotaOnce(ctx, target)
				}(target)
			}
			for id := range lastPolled {
				if _, ok := current[id]; !ok {
					delete(lastPolled, id)
				}
			}
		}
	}
}

func (m *Manager) pollQuotaOnce(ctx context.Context, target quotaPollTarget) {
	pollStart := time.Now()
	baseline := m.cooldownStateRecordsForAuthLocked(target.auth, pollStart)
	headers, errPoll := target.poller.PollQuota(ctx, target.auth)
	if errPoll != nil {
		if ctx.Err() == nil {
			log.WithField("auth_id", target.auth.ID).Debugf("quota poll failed: %v", errPoll)
		}
		return
	}
	if len(headers) == 0 {
		return
	}
	now := time.Now()
	m.observePolledQuota(target.auth.ID, headers, now)
	m.clearCooldownIfPollShowsCapacity(ctx, target.auth, headers, pollStart, baseline, now)
}

// clearCooldownIfPollShowsCapacity lifts a quota cooldown once the polled usage
// shows room. The guard compares cooldown records with the pre-poll baseline
// under m.mu, so a failure that landed while the poll was in flight keeps its
// block; the next poll retries.
func (m *Manager) clearCooldownIfPollShowsCapacity(ctx context.Context, target *Auth, headers http.Header, pollStart time.Time, baseline []CooldownStateRecord, now time.Time) {
	if m.HomeEnabled() {
		return
	}
	var polled QuotaState
	if !polled.ObserveResponseHeadersForProvider(target.Provider, headers, now) || !pollShowsQuotaCapacity(target.Provider, polled) {
		return
	}
	guard := func(auth *Auth) bool {
		return hasActiveQuotaCooldown(auth, now) &&
			cooldownRecordsUnchanged(baseline, m.cooldownStateRecordsForAuthLocked(auth, pollStart))
	}
	cleared, _, errReset := m.resetQuota(ctx, target.ID, guard)
	if errReset != nil {
		log.WithFields(log.Fields{"auth_id": target.ID, "provider": target.Provider}).Warnf("quota poll cooldown clear failed: %v", errReset)
		return
	}
	if cleared == nil {
		return
	}
	fields := log.Fields{"auth_id": target.ID, "provider": target.Provider}
	for _, window := range quotaWindowsFromSnapshot(strings.ToLower(strings.TrimSpace(target.Provider)), polled, false) {
		fields["used_percent_"+window.Class] = window.UsedPercent
	}
	log.WithFields(fields).Info("quota poll shows capacity; cleared quota cooldown")
}

// observePolledQuota records polled quota headers as the credential-level
// observation snapshot, exactly as response headers from real traffic would be.
// Cooldown and scheduling state is left untouched.
func (m *Manager) observePolledQuota(authID string, headers http.Header, now time.Time) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	auth := m.auths[authID]
	if auth == nil || !auth.Quota.ObserveResponseHeadersForProvider(auth.Provider, headers, now) {
		m.mu.Unlock()
		return false
	}
	auth.Generation++
	auth.UpdatedAt = now
	snapshot := auth.Clone()
	persist := m.cooldownStore != nil && now.Sub(m.observationPersistedAt) >= quotaObservationPersistInterval
	if persist {
		m.observationPersistedAt = now
	}
	m.mu.Unlock()

	if m.scheduler != nil {
		m.scheduler.upsertAuthResult(snapshot, nil, true)
	}
	if persist {
		m.persistCooldownStates(context.Background())
	}
	return true
}
