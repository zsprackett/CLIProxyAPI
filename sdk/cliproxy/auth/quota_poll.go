package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	log "github.com/sirupsen/logrus"
)

var (
	// ErrQuotaNotPollable says the credential has no usage endpoint, such as an
	// API key or a setup token.
	ErrQuotaNotPollable = errors.New("quota: credential is not pollable")
	// ErrQuotaPollHomeMode says polls are refused while the Home control plane runs.
	ErrQuotaPollHomeMode = errors.New("quota: polling is disabled in Home mode")
	// ErrQuotaAuthNotFound says the manager does not know the credential.
	ErrQuotaAuthNotFound = errors.New("quota: auth not found")
)

// QuotaPoller is implemented by executors that can fetch a credential's current
// quota without consuming it. A non-nil error with a provider in the
// observation means no section succeeded; the failed sections still land in
// the snapshot. ErrQuotaNotPollable means the credential has nothing to poll.
type QuotaPoller interface {
	PollQuota(ctx context.Context, auth *Auth, trigger PollTrigger) (QuotaObservation, error)
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

// quotaPollCall is one poll in flight. Callers that join it wait on done and
// read its result.
type quotaPollCall struct {
	done     chan struct{}
	snapshot QuotaSnapshot
	err      error
}

// quotaPollState is the poll bookkeeping the background loop and PollQuotaNow
// share: one call in flight per credential, and when each was last polled, so
// an on-demand poll defers the next background one.
type quotaPollState struct {
	mu         sync.Mutex
	lastPolled map[string]time.Time
	inFlight   map[string]*quotaPollCall
	nowFunc    func() time.Time
	// joined runs when a caller joins a poll in flight. Tests set it.
	joined func()
}

func (s *quotaPollState) now() time.Time {
	if s.nowFunc != nil {
		return s.nowFunc()
	}
	return time.Now()
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

// quotaPollDue reports whether a credential last polled at lastPolled should be
// polled at now. The loop only wakes every quotaPollCheckInterval, so a strict
// comparison would round every interval up to the next tick; the half-tick
// tolerance rounds it to the nearest tick instead.
func quotaPollDue(now, lastPolled time.Time, interval time.Duration) bool {
	if interval <= 0 {
		return false
	}
	if lastPolled.IsZero() {
		return true
	}
	return now.Sub(lastPolled) >= interval-quotaPollCheckInterval/2
}

// quotaPollTargets snapshots the enabled credentials whose executor supports quota polling.
func (m *Manager) quotaPollTargets() []quotaPollTarget {
	m.mu.RLock()
	defer m.mu.RUnlock()
	targets := make([]quotaPollTarget, 0, len(m.auths))
	for _, auth := range m.auths {
		if auth == nil || auth.Disabled || auth.Status == StatusDisabled {
			continue
		}
		if target, ok := m.quotaPollTargetLocked(auth); ok {
			targets = append(targets, target)
		}
	}
	return targets
}

func (m *Manager) quotaPollTargetLocked(auth *Auth) (quotaPollTarget, bool) {
	if !ProviderSupportsQuotaObservation(auth.Provider) {
		return quotaPollTarget{}, false
	}
	exec, ok := m.executorLocked(executorKeyFromAuth(auth))
	if !ok || exec == nil {
		return quotaPollTarget{}, false
	}
	poller, ok := exec.(QuotaPoller)
	if !ok {
		return quotaPollTarget{}, false
	}
	return quotaPollTarget{auth: auth.Clone(), poller: poller}, true
}

// runQuotaPoll polls each credential's quota at the configured
// quota-poll-interval-seconds until ctx is cancelled.
func (m *Manager) runQuotaPoll(ctx context.Context) {
	ticker := time.NewTicker(quotaPollCheckInterval)
	defer ticker.Stop()
	m.runQuotaPollTicks(ctx, ticker.C)
}

// runQuotaPollTicks is the poll loop body. Each credential is polled
// independently so one slow upstream never delays the others.
func (m *Manager) runQuotaPollTicks(ctx context.Context, ticks <-chan time.Time) {
	slots := make(chan struct{}, quotaPollMaxConcurrency)
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticks:
			interval := m.quotaPollInterval()
			if interval <= 0 {
				continue
			}
			targets := m.quotaPollTargets()
			current := make(map[string]struct{}, len(targets))
			for _, target := range targets {
				id := target.auth.ID
				current[id] = struct{}{}
				if !m.quotaPoll.due(id, now, interval) {
					continue
				}
				select {
				case slots <- struct{}{}:
				default:
					// All slots busy; this credential is retried on the next tick.
					continue
				}
				go func(target quotaPollTarget) {
					defer func() { <-slots }()
					if _, errPoll := m.pollQuota(ctx, target, PollBackground, now); errPoll != nil && ctx.Err() == nil {
						log.WithField("auth_id", target.auth.ID).Debugf("quota poll failed: %v", errPoll)
					}
				}(target)
			}
			m.quotaPoll.prune(current)
		}
	}
}

// due reports whether the credential should be polled now and is not in flight.
func (s *quotaPollState) due(id string, now time.Time, interval time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, busy := s.inFlight[id]; busy {
		return false
	}
	return quotaPollDue(now, s.lastPolled[id], interval)
}

// prune forgets credentials that are no longer poll targets.
func (s *quotaPollState) prune(current map[string]struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.lastPolled {
		if _, ok := current[id]; !ok {
			delete(s.lastPolled, id)
		}
	}
}

// PollQuotaNow polls one credential at once and returns its snapshot. It joins
// a poll of the same credential that is already in flight. An upstream failure
// is not an error: the snapshot carries it in LastPoll.Error.
func (m *Manager) PollQuotaNow(ctx context.Context, authID string) (QuotaSnapshot, error) {
	if m == nil {
		return QuotaSnapshot{}, errors.New("auth manager is nil")
	}
	if m.HomeEnabled() {
		return QuotaSnapshot{}, ErrQuotaPollHomeMode
	}
	m.mu.RLock()
	auth := m.auths[strings.TrimSpace(authID)]
	if auth == nil {
		m.mu.RUnlock()
		return QuotaSnapshot{}, ErrQuotaAuthNotFound
	}
	target, ok := m.quotaPollTargetLocked(auth)
	m.mu.RUnlock()
	if !ok {
		return QuotaSnapshot{}, ErrQuotaNotPollable
	}
	return m.pollQuota(ctx, target, PollOnDemand, m.quotaPoll.now())
}

// pollQuota runs one poll of target, or joins the one in flight. The caller
// that starts the poll stamps lastPolled, so a click resets the background timer.
func (m *Manager) pollQuota(ctx context.Context, target quotaPollTarget, trigger PollTrigger, now time.Time) (QuotaSnapshot, error) {
	id := target.auth.ID
	state := &m.quotaPoll
	state.mu.Lock()
	if call, busy := state.inFlight[id]; busy {
		state.mu.Unlock()
		if state.joined != nil {
			state.joined()
		}
		select {
		case <-call.done:
			return call.snapshot, call.err
		case <-ctx.Done():
			return QuotaSnapshot{}, ctx.Err()
		}
	}
	if state.inFlight == nil {
		state.inFlight = make(map[string]*quotaPollCall)
		state.lastPolled = make(map[string]time.Time)
	}
	call := &quotaPollCall{done: make(chan struct{})}
	state.inFlight[id] = call
	state.lastPolled[id] = now
	state.mu.Unlock()

	call.snapshot, call.err = m.pollQuotaOnce(ctx, target, trigger)

	state.mu.Lock()
	delete(state.inFlight, id)
	state.mu.Unlock()
	close(call.done)
	return call.snapshot, call.err
}

func (m *Manager) pollQuotaOnce(ctx context.Context, target quotaPollTarget, trigger PollTrigger) (QuotaSnapshot, error) {
	pollStart := m.quotaPoll.now()
	baseline := m.cooldownStateRecordsForAuthLocked(target.auth, pollStart)
	obs, errPoll := target.poller.PollQuota(ctx, target.auth, trigger)
	if errors.Is(errPoll, ErrQuotaNotPollable) {
		return QuotaSnapshot{}, errPoll
	}
	now := m.quotaPoll.now()
	if obs.Provider == "" {
		kind, ok := providerKindOf(target.auth.Provider)
		if !ok {
			return QuotaSnapshot{}, ErrQuotaNotPollable
		}
		obs = QuotaObservation{Provider: kind, Source: SourcePoll}
	}
	if obs.ObservedAt.IsZero() {
		obs.ObservedAt = now
	}
	obs.Poll = &PollStatus{At: now, Trigger: trigger}
	if errPoll != nil {
		obs.Poll.Error = boundedSectionError(errPoll.Error())
	}
	snapshot, found := m.observePolledQuota(target.auth.ID, obs)
	if !found {
		return QuotaSnapshot{}, ErrQuotaAuthNotFound
	}
	if errPoll == nil {
		m.clearCooldownIfPollShowsCapacity(ctx, target.auth, obs, pollStart, baseline, now)
	}
	return snapshot, nil
}

// clearCooldownIfPollShowsCapacity lifts a quota cooldown once the polled usage
// shows room. The guard compares cooldown records with the pre-poll baseline
// under m.mu, so a failure that landed while the poll was in flight keeps its
// block; the next poll retries.
func (m *Manager) clearCooldownIfPollShowsCapacity(ctx context.Context, target *Auth, obs QuotaObservation, pollStart time.Time, baseline []CooldownStateRecord, now time.Time) {
	if m.HomeEnabled() || !pollShowsQuotaCapacity(obs) {
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
	for _, window := range obs.Windows {
		if used, known := window.Used.Get(); known {
			if class := window.Class(obs.Provider); class != "" {
				fields["used_percent_"+string(class)] = used
			}
		}
	}
	log.WithFields(fields).Info("quota poll shows capacity; cleared quota cooldown")
}

// observePolledQuota applies a poll observation to the credential's snapshot
// and returns the result. Cooldown and scheduling state is left untouched.
// found is false when the manager no longer holds the credential.
func (m *Manager) observePolledQuota(authID string, obs QuotaObservation) (QuotaSnapshot, bool) {
	if m == nil {
		return QuotaSnapshot{}, false
	}
	now := obs.ObservedAt
	if now.IsZero() {
		now = time.Now()
	}
	m.mu.Lock()
	auth := m.auths[authID]
	if auth == nil {
		m.mu.Unlock()
		return QuotaSnapshot{}, false
	}
	if !auth.QuotaSnapshot.apply(obs) {
		snapshot := auth.QuotaSnapshot.Clone()
		m.mu.Unlock()
		return snapshot, true
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
	return snapshot.QuotaSnapshot, true
}

/* ------------------------------------------------------- temporary adapter */

// XAIBillingPollObservation turns the x-xai-billing-* header form the P1 xAI
// poller still returns into a poll observation. P3 deletes it when the xAI
// poller returns a QuotaObservation directly.
func XAIBillingPollObservation(headers http.Header, now time.Time) (QuotaObservation, bool) {
	return xaiBillingHeadersObservation(headers, now)
}

// xaiBillingHeadersObservation reads the x-xai-billing-* form the P1 xAI poller
// synthesizes from the Grok billing endpoints. The legacy .cds restore uses it too.
// The reading carries the active billing period only, so any successful reading
// marks both xAI sections ok and retires the other period's window. P3 replaces
// this adapter with independent sections.
func xaiBillingHeadersObservation(headers http.Header, now time.Time) (QuotaObservation, bool) {
	used, hasUsed := parseQuotaFloat(quotaHeaderValue(headers, "X-Xai-Billing-Used-Percent"))
	resetAt := parseQuotaResetAt(quotaHeaderValue(headers, "X-Xai-Billing-Reset-At"), "", now)
	if !hasUsed && resetAt.IsZero() {
		return QuotaObservation{}, false
	}
	id, section, period := WindowID("weekly"), SectionXAIWeekly, 7*24*time.Hour
	sections := map[SectionID]SectionStatus{SectionXAIWeekly: SectionOK, SectionXAIMonthly: SectionOK}
	if strings.EqualFold(quotaHeaderValue(headers, "X-Xai-Billing-Period-Type"), "monthly") {
		id, section, period = "monthly", SectionXAIMonthly, 30*24*time.Hour
	}
	if minutes, ok := parseQuotaFloat(quotaHeaderValue(headers, "X-Xai-Billing-Window-Minutes")); ok && minutes > 0 {
		period = time.Duration(minutes) * time.Minute
	}
	window := QuotaWindowObs{ID: id, ResetAt: resetAt, Period: period, Section: section, Source: SourcePoll, ObservedAt: now}
	if hasUsed {
		window.Used = KnownPercent(used)
	}
	return QuotaObservation{
		Provider:   ProviderXAI,
		Source:     SourcePoll,
		ObservedAt: now,
		Windows:    []QuotaWindowObs{window},
		Sections:   sections,
		Extras:     XAIExtras{Access: XAIAccessGrokCLI},
	}, true
}

// DevinStatusObservation builds the Devin observation from GetUserStatus
// fields. The token refresh and the legacy .cds restore share it. P3 moves the
// parser next to the Devin poller.
func DevinStatusObservation(source Source, plan string, dailyRemaining, weeklyRemaining int64, dailyReset, weeklyReset, planStart, planEnd, now time.Time) QuotaObservation {
	obs := QuotaObservation{
		Provider:   ProviderDevin,
		Source:     source,
		ObservedAt: now,
		Sections:   map[SectionID]SectionStatus{SectionDevinStatus: SectionOK},
		Windows: []QuotaWindowObs{
			{ID: "daily", Used: KnownPercent(float64(100 - dailyRemaining)), ResetAt: dailyReset, Period: 24 * time.Hour, Section: SectionDevinStatus, Source: source, ObservedAt: now},
			{ID: "weekly", Used: KnownPercent(float64(100 - weeklyRemaining)), ResetAt: weeklyReset, Period: 7 * 24 * time.Hour, Section: SectionDevinStatus, Source: source, ObservedAt: now},
		},
	}
	extras := DevinExtras{}
	if strings.TrimSpace(plan) != "" {
		extras.Plan = OKSection(DevinPlan{Name: plan, Start: Instant{planStart}, End: Instant{planEnd}}, now)
	}
	obs.Extras = extras
	return obs
}

// devinLegacySignals converts the Devin keys the pre-snapshot refresh path
// stored in quota.signals. Deleted with the legacy decoder after one release.
func devinLegacySignals(signals map[string]string, observedAt time.Time) (QuotaObservation, bool) {
	percent := func(key string) (int64, bool) {
		value, ok := parseQuotaFloat(strings.TrimSuffix(strings.TrimSpace(signals[key]), "%"))
		if !ok {
			return 0, false
		}
		return int64(value), true
	}
	daily, okDaily := percent("daily_quota_remaining_percent")
	weekly, okWeekly := percent("weekly_quota_remaining_percent")
	if !okDaily && !okWeekly {
		return QuotaObservation{}, false
	}
	instant := func(key string) time.Time {
		return parseQuotaResetAt(strings.TrimSpace(signals[key]), "", observedAt)
	}
	obs := DevinStatusObservation(SourceRefresh, signals["plan"], daily, weekly,
		instant("daily_quota_reset_at"), instant("weekly_quota_reset_at"), instant("plan_start"), instant("plan_end"), observedAt)
	if !okDaily {
		obs.Windows = obs.Windows[1:]
	} else if !okWeekly {
		obs.Windows = obs.Windows[:1]
	}
	return obs, true
}
