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
// passive response-header observation. A nil header with a nil error means the
// credential has nothing to poll (e.g. API keys).
//
// P2 and P3 change this to return a QuotaObservation and delete fromHeadersAdapter.
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
	obs, ok := fromHeadersAdapter(target.auth.Provider, headers, now)
	if !ok {
		return
	}
	obs.Poll = &PollStatus{At: now, Trigger: PollBackground}
	m.observePolledQuota(target.auth.ID, obs)
	m.clearCooldownIfPollShowsCapacity(ctx, target.auth, obs, pollStart, baseline, now)
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

// observePolledQuota applies a poll observation to the credential's snapshot.
// Cooldown and scheduling state is left untouched.
func (m *Manager) observePolledQuota(authID string, obs QuotaObservation) bool {
	if m == nil {
		return false
	}
	now := obs.ObservedAt
	if now.IsZero() {
		now = time.Now()
	}
	m.mu.Lock()
	auth := m.auths[authID]
	if auth == nil || !auth.QuotaSnapshot.apply(obs) {
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

/* ------------------------------------------------------- temporary adapter */

// claudePollFableScope is the scope of the 7d_oi block of the Claude usage
// payload, which describes the Fable model family.
const claudePollFableScope = "fable"

// fromHeadersAdapter turns the header form the P1 pollers still return into a
// poll observation. Claude and Codex go through the passive header parsers;
// xAI has its own billing header form. Each window is attributed to the
// provider's poll section, so the next poll can retire a window it no longer
// returns without touching header-sourced windows.
//
// P2 (Claude, Codex) and P3 (xAI) delete this adapter when the pollers return a
// QuotaObservation directly.
func fromHeadersAdapter(provider string, headers http.Header, now time.Time) (QuotaObservation, bool) {
	kind, ok := providerKindOf(provider)
	if !ok {
		return QuotaObservation{}, false
	}
	var obs QuotaObservation
	var section SectionID
	switch kind {
	case ProviderClaude:
		obs, ok = claudeHeadersObservationScoped(headers, claudePollFableScope, now)
		section = SectionClaudeUsage
	case ProviderCodex:
		obs, ok = codexHeadersObservation(headers, "", now)
		section = SectionCodexUsage
	case ProviderXAI:
		obs, ok = xaiBillingHeadersObservation(headers, now)
		section = ""
	default:
		return QuotaObservation{}, false
	}
	if !ok {
		return QuotaObservation{}, false
	}
	obs.Source = SourcePoll
	if section != "" {
		obs.Sections = map[SectionID]SectionStatus{section: SectionOK}
		for i := range obs.Windows {
			obs.Windows[i].Section = section
			obs.Windows[i].Source = SourcePoll
		}
	}
	return obs, true
}

// xaiBillingHeadersObservation reads the x-xai-billing-* form the P1 xAI poller
// synthesizes from the Grok billing endpoints. The legacy .cds restore uses it too.
// The poller returns a monthly period only when the weekly request succeeded
// without a weekly period, so a monthly reading also retires the weekly window.
func xaiBillingHeadersObservation(headers http.Header, now time.Time) (QuotaObservation, bool) {
	used, hasUsed := parseQuotaFloat(quotaHeaderValue(headers, "X-Xai-Billing-Used-Percent"))
	resetAt := parseQuotaResetAt(quotaHeaderValue(headers, "X-Xai-Billing-Reset-At"), "", now)
	if !hasUsed && resetAt.IsZero() {
		return QuotaObservation{}, false
	}
	id, section, period := WindowID("weekly"), SectionXAIWeekly, 7*24*time.Hour
	sections := map[SectionID]SectionStatus{SectionXAIWeekly: SectionOK}
	if strings.EqualFold(quotaHeaderValue(headers, "X-Xai-Billing-Period-Type"), "monthly") {
		id, section, period = "monthly", SectionXAIMonthly, 30*24*time.Hour
		sections[SectionXAIMonthly] = SectionOK
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
