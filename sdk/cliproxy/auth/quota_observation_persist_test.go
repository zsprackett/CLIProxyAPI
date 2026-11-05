package auth

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
)

func newObservationPersistManager(t *testing.T, store CooldownStateStore, authID string) *Manager {
	t.Helper()
	m := NewManager(nil, nil, nil)
	m.SetCooldownStateStore(store)
	if _, err := m.Register(context.Background(), &Auth{ID: authID, Provider: "claude", Status: StatusActive}); err != nil {
		t.Fatalf("register: %v", err)
	}
	return m
}

func TestQuotaObservationsSurviveRestart(t *testing.T) {
	ctx := context.Background()
	store := NewFileCooldownStateStore(t.TempDir())
	now := time.Now()
	weeklyReset := now.Add(48 * time.Hour).Truncate(time.Second)
	fableReset := now.Add(20 * time.Hour).Truncate(time.Second)

	first := newObservationPersistManager(t, store, "claude-a.json")
	first.mu.Lock()
	first.auths["claude-a.json"].QuotaSnapshot.ObserveResponseHeaders("claude", "claude-fable-5", http.Header{
		"Anthropic-Ratelimit-Unified-7d-Reset":    []string{strconv.FormatInt(weeklyReset.Unix(), 10)},
		"Anthropic-Ratelimit-Unified-7d_oi-Reset": []string{strconv.FormatInt(fableReset.Unix(), 10)},
	}, now.Add(-time.Minute))
	first.mu.Unlock()
	first.persistCooldownStates(ctx)

	second := newObservationPersistManager(t, store, "claude-a.json")
	if err := second.RestoreCooldownStates(ctx); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored, ok := second.GetByID("claude-a.json")
	if !ok {
		t.Fatal("auth missing after restore")
	}
	if got := rankSoonestReset(restored, "", now).weeklyReset; !got.Equal(weeklyReset) {
		t.Fatalf("credential weekly reset = %v, want %v", got, weeklyReset)
	}
	if got := rankSoonestReset(restored, "claude-fable-5", now).weeklyReset; !got.Equal(fableReset) {
		t.Fatalf("fable weekly reset = %v, want %v", got, fableReset)
	}
	if restored.Unavailable {
		t.Fatal("restoring an observation must not make the auth unavailable")
	}
}

func TestQuotaObservationSharesRecordWithCooldown(t *testing.T) {
	ctx := context.Background()
	store := NewFileCooldownStateStore(t.TempDir())
	now := time.Now()
	retryAt := now.Add(time.Hour)

	first := newObservationPersistManager(t, store, "claude-b.json")
	first.mu.Lock()
	cooling := first.auths["claude-b.json"]
	cooling.Unavailable = true
	cooling.Status = StatusError
	cooling.NextRetryAfter = retryAt
	cooling.Quota = QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: retryAt}
	cooling.QuotaSnapshot.ObserveResponseHeaders("claude", "", http.Header{
		"Anthropic-Ratelimit-Unified-7d-Utilization": []string{"1.0"},
	}, now)
	first.mu.Unlock()

	records := first.cooldownStateRecordsSnapshot()
	if len(records) != 1 {
		t.Fatalf("records = %+v, want one combined record", records)
	}
	if records[0].NextRetryAfter.IsZero() || records[0].Snapshot == nil || len(records[0].Snapshot.Windows) != 1 {
		t.Fatalf("record lost cooldown or snapshot: %+v", records[0])
	}
	first.persistCooldownStates(ctx)

	second := newObservationPersistManager(t, store, "claude-b.json")
	if err := second.RestoreCooldownStates(ctx); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored, _ := second.GetByID("claude-b.json")
	if !restored.Unavailable || !restored.NextRetryAfter.Equal(retryAt) {
		t.Fatalf("cooldown not restored: %+v", restored)
	}
	windows := restored.QuotaSnapshot.Windows
	if len(windows) != 1 || windows[0].ID != "seven_day" {
		t.Fatalf("snapshot not restored: %+v", restored.QuotaSnapshot)
	}
	if used, known := windows[0].Used.Get(); !known || used != 100 {
		t.Fatalf("restored 7d used = %v (known %t), want 100", used, known)
	}
}

func TestOldQuotaObservationIsSavedAndRestored(t *testing.T) {
	ctx := context.Background()
	store := NewFileCooldownStateStore(t.TempDir())
	observedAt := time.Now().Add(-9*24*time.Hour - time.Hour)

	first := newObservationPersistManager(t, store, "claude-c.json")
	first.mu.Lock()
	first.auths["claude-c.json"].QuotaSnapshot.ObserveResponseHeaders("claude", "", http.Header{
		"Anthropic-Ratelimit-Unified-7d-Utilization": []string{"0.5"},
	}, observedAt)
	first.mu.Unlock()
	if records := first.cooldownStateRecordsSnapshot(); len(records) != 1 {
		t.Fatalf("old observation was not saved: %+v", records)
	}
	first.persistCooldownStates(ctx)

	second := newObservationPersistManager(t, store, "claude-c.json")
	if err := second.RestoreCooldownStates(ctx); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored, _ := second.GetByID("claude-c.json")
	windows := restored.QuotaSnapshot.Windows
	if len(windows) != 1 || windows[0].ID != "seven_day" {
		t.Fatalf("old observation not restored: %+v", restored.QuotaSnapshot)
	}
	if used, known := windows[0].Used.Get(); !known || used != 50 {
		t.Fatalf("restored 7d used = %v (known %t), want 50", used, known)
	}
}

func TestRestoreKeepsNewerLiveObservation(t *testing.T) {
	ctx := context.Background()
	store := NewFileCooldownStateStore(t.TempDir())
	now := time.Now()

	first := newObservationPersistManager(t, store, "claude-d.json")
	first.mu.Lock()
	first.auths["claude-d.json"].QuotaSnapshot.ObserveResponseHeaders("claude", "", http.Header{
		"Anthropic-Ratelimit-Unified-7d-Utilization": []string{"0.9"},
	}, now.Add(-time.Hour))
	first.mu.Unlock()
	first.persistCooldownStates(ctx)

	second := newObservationPersistManager(t, store, "claude-d.json")
	second.mu.Lock()
	second.auths["claude-d.json"].QuotaSnapshot.ObserveResponseHeaders("claude", "", http.Header{
		"Anthropic-Ratelimit-Unified-7d-Utilization": []string{"0.1"},
	}, now)
	second.mu.Unlock()
	if err := second.RestoreCooldownStates(ctx); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored, _ := second.GetByID("claude-d.json")
	windows := restored.QuotaSnapshot.Windows
	if len(windows) != 1 {
		t.Fatalf("windows = %+v, want one", windows)
	}
	if used, known := windows[0].Used.Get(); !known || math.Abs(used-10) > 1e-9 {
		t.Fatalf("utilization = %v (known %t), want the newer live value 10", used, known)
	}
}

type countingCooldownStore struct {
	saves atomic.Int32
}

func (s *countingCooldownStore) Load(context.Context) ([]CooldownStateRecord, error) {
	return nil, nil
}

func (s *countingCooldownStore) Save(context.Context, []CooldownStateRecord) error {
	s.saves.Add(1)
	return nil
}

func TestMarkResultThrottlesObservationSaves(t *testing.T) {
	store := &countingCooldownStore{}
	m := newObservationPersistManager(t, store, "claude-e.json")
	baseline := store.saves.Load()

	markWithSignals := func(utilization string) {
		ctx := internallogging.WithResponseHeadersHolder(context.Background())
		internallogging.SetResponseHeaders(ctx, http.Header{
			"Anthropic-Ratelimit-Unified-7d-Utilization": []string{utilization},
		})
		m.MarkResult(ctx, Result{AuthID: "claude-e.json", Provider: "claude", Model: "claude-opus", Success: true})
	}

	markWithSignals("0.10")
	if got := store.saves.Load() - baseline; got != 1 {
		t.Fatalf("saves after first observation = %d, want 1", got)
	}
	markWithSignals("0.11")
	markWithSignals("0.12")
	if got := store.saves.Load() - baseline; got != 1 {
		t.Fatalf("saves within the throttle interval = %d, want 1", got)
	}

	m.mu.Lock()
	m.observationPersistedAt = time.Now().Add(-quotaObservationPersistInterval)
	m.mu.Unlock()
	markWithSignals("0.13")
	if got := store.saves.Load() - baseline; got != 2 {
		t.Fatalf("saves after the throttle interval = %d, want 2", got)
	}
}

func TestRestoreKeepsLastGoodValueOfSavedFailedSection(t *testing.T) {
	ctx := context.Background()
	store := NewFileCooldownStateStore(t.TempDir())
	valueAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	checkedAt := valueAt.Add(30 * time.Minute)
	weekly := XAIWeekly{PeriodStart: Instant{Time: valueAt}, PeriodEnd: Instant{Time: valueAt.Add(7 * 24 * time.Hour)}}

	newManager := func() *Manager {
		m := NewManager(nil, nil, nil)
		m.SetCooldownStateStore(store)
		if _, err := m.Register(ctx, &Auth{ID: "xai-a.json", Provider: "xai", Status: StatusActive}); err != nil {
			t.Fatalf("register: %v", err)
		}
		return m
	}
	first := newManager()
	first.mu.Lock()
	first.auths["xai-a.json"].QuotaSnapshot.apply(QuotaObservation{Provider: ProviderXAI, Source: SourcePoll, ObservedAt: valueAt,
		Extras: XAIExtras{Weekly: OKSection(weekly, valueAt)}})
	first.auths["xai-a.json"].QuotaSnapshot.apply(QuotaObservation{Provider: ProviderXAI, Source: SourcePoll, ObservedAt: checkedAt,
		Extras: XAIExtras{Weekly: FailedSection[XAIWeekly]("503", checkedAt)}})
	first.mu.Unlock()
	first.persistCooldownStates(ctx)

	second := newManager()
	if err := second.RestoreCooldownStates(ctx); err != nil {
		t.Fatalf("restore: %v", err)
	}
	restored, ok := second.GetByID("xai-a.json")
	if !ok {
		t.Fatal("auth missing after restore")
	}
	extras, _ := restored.QuotaSnapshot.Extras.(XAIExtras)
	got := extras.Weekly
	if got.Status != SectionFailed {
		t.Fatalf("weekly status = %q, want failed: %+v", got.Status, got)
	}
	if got.Value == nil || !got.ValueAt.Equal(valueAt) {
		t.Fatalf("weekly lost its last good value: %+v", got)
	}
}

type snapshotCooldownStore struct {
	mu   sync.Mutex
	last []CooldownStateRecord
	n    int
}

func (s *snapshotCooldownStore) Load(context.Context) ([]CooldownStateRecord, error) {
	return nil, nil
}

func (s *snapshotCooldownStore) Save(_ context.Context, records []CooldownStateRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.n++
	s.last = records
	return nil
}

func (s *snapshotCooldownStore) saves() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

// savedSevenDayUsed returns the 7d utilization in the last saved snapshot.
func (s *snapshotCooldownStore) savedSevenDayUsed(t *testing.T) float64 {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, record := range s.last {
		if record.Snapshot == nil {
			continue
		}
		for _, window := range record.Snapshot.Windows {
			if window.ID == "seven_day" {
				used, _ := window.Used.Get()
				return used
			}
		}
	}
	t.Fatalf("no saved 7d window in %+v", s.last)
	return 0
}

// fakeTimers captures the trailing-save callbacks instead of starting real timers.
type fakeTimers struct {
	delays    []time.Duration
	callbacks []func()
}

func (f *fakeTimers) afterFunc(d time.Duration, fn func()) {
	f.delays = append(f.delays, d)
	f.callbacks = append(f.callbacks, fn)
}

func TestMarkResultSavesLastThrottledObservationWhenIntervalEnds(t *testing.T) {
	store := &snapshotCooldownStore{}
	m := newObservationPersistManager(t, store, "claude-f.json")
	timers := &fakeTimers{}
	m.afterFunc = timers.afterFunc
	baseline := store.saves()

	mark := func(utilization string) {
		ctx := internallogging.WithResponseHeadersHolder(context.Background())
		internallogging.SetResponseHeaders(ctx, http.Header{
			"Anthropic-Ratelimit-Unified-7d-Utilization": []string{utilization},
		})
		m.MarkResult(ctx, Result{AuthID: "claude-f.json", Provider: "claude", Model: "claude-opus", Success: true})
	}

	mark("0.10")
	if got := store.saves() - baseline; got != 1 || len(timers.callbacks) != 0 {
		t.Fatalf("first observation: saves = %d, timers = %d, want 1 and 0", got, len(timers.callbacks))
	}
	mark("0.11")
	if got := store.saves() - baseline; got != 1 {
		t.Fatalf("saves after a throttled observation = %d, want 1", got)
	}
	if len(timers.callbacks) != 1 {
		t.Fatalf("trailing saves scheduled = %d, want 1", len(timers.callbacks))
	}
	if d := timers.delays[0]; d <= 0 || d > quotaObservationPersistInterval {
		t.Fatalf("trailing save delay = %v, want within (0, %v]", d, quotaObservationPersistInterval)
	}
	mark("0.12")
	if len(timers.callbacks) != 1 {
		t.Fatalf("trailing saves scheduled after a third observation = %d, want 1", len(timers.callbacks))
	}

	timers.callbacks[0]()
	if got := store.saves() - baseline; got != 2 {
		t.Fatalf("saves after the trailing save fired = %d, want 2", got)
	}
	if used := store.savedSevenDayUsed(t); math.Abs(used-12) > 1e-9 {
		t.Fatalf("trailing save wrote 7d used = %v, want the last observation 12", used)
	}

	mark("0.13")
	if len(timers.callbacks) != 2 {
		t.Fatalf("a new throttled observation after the fire scheduled %d trailing saves in total, want 2", len(timers.callbacks))
	}
}

func TestPolledQuotaSavesLastThrottledObservationWhenIntervalEnds(t *testing.T) {
	store := &snapshotCooldownStore{}
	m := newObservationPersistManager(t, store, "claude-g.json")
	timers := &fakeTimers{}
	m.afterFunc = timers.afterFunc
	baseline := store.saves()

	poll := func(used float64) {
		obs := QuotaObservation{
			Provider:   ProviderClaude,
			Source:     SourcePoll,
			ObservedAt: time.Now(),
			Windows: []QuotaWindowObs{{
				ID:         "seven_day",
				Period:     7 * 24 * time.Hour,
				Used:       KnownPercent(used),
				Source:     SourcePoll,
				ObservedAt: time.Now(),
			}},
		}
		if _, found := m.observePolledQuota("claude-g.json", obs); !found {
			t.Fatal("auth not found")
		}
	}

	poll(10)
	if got := store.saves() - baseline; got != 1 || len(timers.callbacks) != 0 {
		t.Fatalf("first poll: saves = %d, timers = %d, want 1 and 0", got, len(timers.callbacks))
	}
	poll(11)
	poll(12)
	if got := store.saves() - baseline; got != 1 {
		t.Fatalf("saves after throttled polls = %d, want 1", got)
	}
	if len(timers.callbacks) != 1 {
		t.Fatalf("trailing saves scheduled = %d, want 1", len(timers.callbacks))
	}

	timers.callbacks[0]()
	if got := store.saves() - baseline; got != 2 {
		t.Fatalf("saves after the trailing save fired = %d, want 2", got)
	}
	if used := store.savedSevenDayUsed(t); math.Abs(used-12) > 1e-9 {
		t.Fatalf("trailing save wrote 7d used = %v, want the last poll 12", used)
	}
}

func TestThrottledObservationWithoutCooldownStoreSchedulesNothing(t *testing.T) {
	m := NewManager(nil, nil, nil)
	timers := &fakeTimers{}
	m.afterFunc = timers.afterFunc
	m.mu.Lock()
	m.observationPersistedAt = time.Now()
	due := m.observationSaveDueLocked(time.Now())
	m.mu.Unlock()
	if due || len(timers.callbacks) != 0 {
		t.Fatalf("due = %v, timers = %d, want false and 0", due, len(timers.callbacks))
	}
}
