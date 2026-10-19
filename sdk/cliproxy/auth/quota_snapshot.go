package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"
)

// ProviderKind names a provider whose quota the backend parses into a snapshot.
type ProviderKind string

const (
	ProviderClaude ProviderKind = "claude"
	ProviderCodex  ProviderKind = "codex"
	ProviderXAI    ProviderKind = "xai"
	ProviderDevin  ProviderKind = "devin"
)

// Percent is a nullable 0-100 value. The zero value is unknown and marshals as null.
type Percent struct {
	v  float64
	ok bool
}

// KnownPercent returns a known percent clamped to 0-100.
func KnownPercent(v float64) Percent {
	return Percent{v: clampPercent(v), ok: true}
}

// Get returns the value and whether it is known.
func (p Percent) Get() (float64, bool) { return p.v, p.ok }

// MarshalJSON writes null for an unknown percent.
func (p Percent) MarshalJSON() ([]byte, error) {
	if !p.ok {
		return []byte("null"), nil
	}
	return json.Marshal(p.v)
}

// UnmarshalJSON reads null as unknown.
func (p *Percent) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*p = Percent{}
		return nil
	}
	var v float64
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	*p = KnownPercent(v)
	return nil
}

// Instant is a time that marshals as null when zero, so the JSON contract can
// say "unknown" without a sentinel date.
type Instant struct{ time.Time }

// MarshalJSON writes null for the zero time.
func (i Instant) MarshalJSON() ([]byte, error) {
	return json.Marshal(jsonTime(i.Time))
}

// UnmarshalJSON reads null as the zero time.
func (i *Instant) UnmarshalJSON(data []byte) error {
	var raw *string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	parsed, err := parseJSONTime(raw)
	if err != nil {
		return err
	}
	i.Time = parsed
	return nil
}

// WindowID is stable per provider: "five_hour", "seven_day_fable", "primary",
// "code_review.secondary", "additional.<limit_name>.primary", "weekly", "daily".
type WindowID string

// SectionID names the upstream sub-request that owns a window or an extras field.
type SectionID string

const (
	SectionHeaders     SectionID = "headers"
	SectionClaudeUsage SectionID = "claude.usage"
	SectionCodexUsage  SectionID = "codex.usage"
	SectionXAIWeekly   SectionID = "xai.weekly"
	SectionXAIMonthly  SectionID = "xai.monthly"
	SectionDevinStatus SectionID = "devin.status"
)

// Source says which path produced an observation.
type Source string

const (
	SourcePoll    Source = "poll"
	SourceHeaders Source = "headers"
	SourceRefresh Source = "refresh"
)

// WindowClass identifies windows that pool across credentials of one provider,
// such as "claude:7d". It is derived from the window, never stored.
type WindowClass string

// QuotaWindowObs is one observed quota window.
type QuotaWindowObs struct {
	ID           WindowID
	Label        string
	ModelScope   string
	Used         Percent
	LimitReached bool
	ResetAt      time.Time
	Period       time.Duration
	Section      SectionID
	Source       Source
	ObservedAt   time.Time
}

// Class returns the pool class of the window under provider p, or "" for a
// display-only window.
func (w QuotaWindowObs) Class(p ProviderKind) WindowClass {
	row, ok := quotaProviders[p]
	if !ok || row.classOf == nil {
		return ""
	}
	return row.classOf(w.ID, w.Period)
}

// SectionStatus is the outcome of one upstream sub-request.
type SectionStatus string

const (
	SectionOK            SectionStatus = "ok"
	SectionFailed        SectionStatus = "failed"
	SectionAbsent        SectionStatus = "absent"
	SectionNotApplicable SectionStatus = "not_applicable"
)

// Section is one sub-request's result. Value is the last good value: it survives
// a later failure, so the UI shows "stale since" instead of a substitute.
type Section[T any] struct {
	Status    SectionStatus
	Value     *T
	ValueAt   time.Time
	CheckedAt time.Time
	Error     string
}

// OKSection builds a successful section observed at the given time.
func OKSection[T any](value T, at time.Time) Section[T] {
	return Section[T]{Status: SectionOK, Value: &value, ValueAt: at, CheckedAt: at}
}

// FailedSection records a failed sub-request without a value.
func FailedSection[T any](err string, at time.Time) Section[T] {
	return Section[T]{Status: SectionFailed, Error: boundedSectionError(err), CheckedAt: at}
}

const maxSectionErrorLength = 256

func boundedSectionError(err string) string {
	err = strings.TrimSpace(err)
	if len(err) > maxSectionErrorLength {
		return err[:maxSectionErrorLength]
	}
	return err
}

func (s Section[T]) status() SectionStatus {
	if s.Status == "" {
		return SectionAbsent
	}
	return s.Status
}

type sectionWire[T any] struct {
	Status    SectionStatus `json:"status"`
	Value     *T            `json:"value"`
	ValueAt   *string       `json:"value_at"`
	CheckedAt *string       `json:"checked_at"`
	Error     string        `json:"error,omitempty"`
}

// MarshalJSON writes null for zero times and "absent" for the zero status.
func (s Section[T]) MarshalJSON() ([]byte, error) {
	return json.Marshal(sectionWire[T]{
		Status:    s.status(),
		Value:     s.Value,
		ValueAt:   jsonTime(s.ValueAt),
		CheckedAt: jsonTime(s.CheckedAt),
		Error:     s.Error,
	})
}

// UnmarshalJSON is the inverse of MarshalJSON.
func (s *Section[T]) UnmarshalJSON(data []byte) error {
	var wire sectionWire[T]
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	valueAt, err := parseJSONTime(wire.ValueAt)
	if err != nil {
		return err
	}
	checkedAt, err := parseJSONTime(wire.CheckedAt)
	if err != nil {
		return err
	}
	*s = Section[T]{Status: wire.Status, Value: wire.Value, ValueAt: valueAt, CheckedAt: checkedAt, Error: wire.Error}
	return nil
}

// latest is the time of the newest fact the section holds: its value or its
// last check.
func (s Section[T]) latest() time.Time {
	if s.CheckedAt.After(s.ValueAt) {
		return s.CheckedAt
	}
	return s.ValueAt
}

// mergeSection keeps the last good value across a failure and ignores an
// absent section, so a partial observation never erases what another
// sub-request learned. Observations apply in ObservedAt order, not arrival
// order, so an ok, a failure, or a not_applicable older than the stored
// section's latest fact is ignored.
func mergeSection[T any](prev, next Section[T]) Section[T] {
	switch next.status() {
	case SectionOK:
		if next.ValueAt.Before(prev.latest()) {
			return prev
		}
		return next
	case SectionFailed:
		if !next.CheckedAt.IsZero() && next.CheckedAt.Before(prev.latest()) {
			return prev
		}
		out := prev
		out.Status = SectionFailed
		out.Error = next.Error
		out.CheckedAt = next.CheckedAt
		return out
	case SectionNotApplicable:
		if !next.CheckedAt.IsZero() && next.CheckedAt.Before(prev.latest()) {
			return prev
		}
		return Section[T]{Status: SectionNotApplicable, CheckedAt: next.CheckedAt}
	default:
		return prev
	}
}

// cloneSection copies a section whose value holds no pointers or slices.
func cloneSection[T any](s Section[T]) Section[T] {
	if s.Value != nil {
		value := *s.Value
		s.Value = &value
	}
	return s
}

// deepCloner is a section value that owns pointers or slices.
type deepCloner[T any] interface{ clone() T }

// cloneSectionDeep copies a section through the value's own clone.
func cloneSectionDeep[T deepCloner[T]](s Section[T]) Section[T] {
	if s.Value != nil {
		value := (*s.Value).clone()
		s.Value = &value
	}
	return s
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// QuotaExtras is sealed: only the four provider structs below implement it.
type QuotaExtras interface {
	Kind() ProviderKind
	mergeFrom(prev QuotaExtras) QuotaExtras
	clone() QuotaExtras
	isQuotaExtras()
}

// ClaudeExtraUsage is the extra-usage block of /api/oauth/usage.
type ClaudeExtraUsage struct {
	Enabled      bool    `json:"enabled"`
	MonthlyLimit float64 `json:"monthly_limit"`
	UsedCredits  float64 `json:"used_credits"`
	Utilization  Percent `json:"utilization"`
}

// ClaudeExtras holds the Claude fields outside the windows.
type ClaudeExtras struct {
	Plan       Section[string]           `json:"plan"`
	ExtraUsage Section[ClaudeExtraUsage] `json:"extra_usage"`
}

func (ClaudeExtras) Kind() ProviderKind { return ProviderClaude }
func (ClaudeExtras) isQuotaExtras()     {}
func (e ClaudeExtras) clone() QuotaExtras {
	return ClaudeExtras{Plan: cloneSection(e.Plan), ExtraUsage: cloneSection(e.ExtraUsage)}
}
func (e ClaudeExtras) mergeFrom(prev QuotaExtras) QuotaExtras {
	p, _ := prev.(ClaudeExtras)
	return ClaudeExtras{
		Plan:       mergeSection(p.Plan, e.Plan),
		ExtraUsage: mergeSection(p.ExtraUsage, e.ExtraUsage),
	}
}

// CodexAccess is the allowed / limit_reached pair of the main Codex rate limit.
type CodexAccess struct {
	Allowed      *bool `json:"allowed"`
	LimitReached *bool `json:"limit_reached"`
}

func (a CodexAccess) clone() CodexAccess {
	return CodexAccess{Allowed: clonePtr(a.Allowed), LimitReached: clonePtr(a.LimitReached)}
}

// CodexCredits is the credits block of a Codex usage payload.
type CodexCredits struct {
	HasCredits *bool   `json:"has_credits"`
	Unlimited  *bool   `json:"unlimited"`
	Balance    *string `json:"balance"`
}

func (c CodexCredits) clone() CodexCredits {
	return CodexCredits{HasCredits: clonePtr(c.HasCredits), Unlimited: clonePtr(c.Unlimited), Balance: clonePtr(c.Balance)}
}

// CodexSubscription is the /subscriptions result.
type CodexSubscription struct {
	ActiveUntil Instant `json:"active_until"`
}

// CodexResetCredit is one manual rate-limit reset credit.
type CodexResetCredit struct {
	ID        string  `json:"id"`
	Status    string  `json:"status"`
	GrantedAt Instant `json:"granted_at"`
	ExpiresAt Instant `json:"expires_at"`
}

// CodexResetCredits is the /wham/rate-limit-reset-credits result.
type CodexResetCredits struct {
	Available           *int               `json:"available"`
	ApplicableAvailable *int               `json:"applicable_available"`
	Credits             []CodexResetCredit `json:"credits"`
}

func (c CodexResetCredits) clone() CodexResetCredits {
	return CodexResetCredits{
		Available:           clonePtr(c.Available),
		ApplicableAvailable: clonePtr(c.ApplicableAvailable),
		Credits:             slices.Clone(c.Credits),
	}
}

// CodexExtras holds the Codex fields outside the windows.
type CodexExtras struct {
	Plan         Section[string]            `json:"plan"`
	Access       Section[CodexAccess]       `json:"access"`
	Credits      Section[CodexCredits]      `json:"credits"`
	Subscription Section[CodexSubscription] `json:"subscription"`
	ResetCredits Section[CodexResetCredits] `json:"reset_credits"`
}

func (CodexExtras) Kind() ProviderKind { return ProviderCodex }
func (CodexExtras) isQuotaExtras()     {}
func (e CodexExtras) clone() QuotaExtras {
	return CodexExtras{
		Plan:         cloneSection(e.Plan),
		Access:       cloneSectionDeep(e.Access),
		Credits:      cloneSectionDeep(e.Credits),
		Subscription: cloneSection(e.Subscription),
		ResetCredits: cloneSectionDeep(e.ResetCredits),
	}
}
func (e CodexExtras) mergeFrom(prev QuotaExtras) QuotaExtras {
	p, _ := prev.(CodexExtras)
	return CodexExtras{
		Plan:         mergeSection(p.Plan, e.Plan),
		Access:       mergeSection(p.Access, e.Access),
		Credits:      mergeSection(p.Credits, e.Credits),
		Subscription: mergeSection(p.Subscription, e.Subscription),
		ResetCredits: mergeSection(p.ResetCredits, e.ResetCredits),
	}
}

// XAIAccess says which upstream path a Grok credential uses.
type XAIAccess string

const (
	XAIAccessGrokCLI XAIAccess = "grok_cli"
	XAIAccessAPIKey  XAIAccess = "api_key"
)

// XAIProductUsage is one product row of the weekly billing payload.
type XAIProductUsage struct {
	Product string  `json:"product"`
	Used    Percent `json:"used"`
}

// XAIWeekly is the weekly billing period without its percent, which lives in window "weekly".
type XAIWeekly struct {
	PeriodStart  Instant           `json:"period_start"`
	PeriodEnd    Instant           `json:"period_end"`
	ProductUsage []XAIProductUsage `json:"product_usage"`
}

func (w XAIWeekly) clone() XAIWeekly {
	w.ProductUsage = slices.Clone(w.ProductUsage)
	return w
}

// XAIMonthly is the monthly billing period in cents.
type XAIMonthly struct {
	LimitCents          *float64 `json:"limit_cents"`
	UsedCents           *float64 `json:"used_cents"`
	IncludedUsedCents   *float64 `json:"included_used_cents"`
	OnDemandCapCents    *float64 `json:"on_demand_cap_cents"`
	OnDemandUsedCents   *float64 `json:"on_demand_used_cents"`
	PrepaidBalanceCents *float64 `json:"prepaid_balance_cents"`
	PeriodStart         Instant  `json:"period_start"`
	PeriodEnd           Instant  `json:"period_end"`
}

func (m XAIMonthly) clone() XAIMonthly {
	m.LimitCents = clonePtr(m.LimitCents)
	m.UsedCents = clonePtr(m.UsedCents)
	m.IncludedUsedCents = clonePtr(m.IncludedUsedCents)
	m.OnDemandCapCents = clonePtr(m.OnDemandCapCents)
	m.OnDemandUsedCents = clonePtr(m.OnDemandUsedCents)
	m.PrepaidBalanceCents = clonePtr(m.PrepaidBalanceCents)
	return m
}

// XAIAccount is the plan and tier of the account.
type XAIAccount struct {
	PlanLabel string `json:"plan_label"`
	Tier      string `json:"tier"`
}

// XAIPaidHealth is the result of the API-key probe.
type XAIPaidHealth struct {
	UserID string `json:"user_id"`
	TeamID string `json:"team_id"`
	ChatOK bool   `json:"chat_ok"`
	Source string `json:"source"`
}

// XAIExtras holds the xAI fields outside the windows.
type XAIExtras struct {
	Access     XAIAccess              `json:"access"`
	Weekly     Section[XAIWeekly]     `json:"weekly"`
	Monthly    Section[XAIMonthly]    `json:"monthly"`
	Account    Section[XAIAccount]    `json:"account"`
	PaidHealth Section[XAIPaidHealth] `json:"paid_health"`
}

func (XAIExtras) Kind() ProviderKind { return ProviderXAI }
func (XAIExtras) isQuotaExtras()     {}
func (e XAIExtras) clone() QuotaExtras {
	return XAIExtras{
		Access:     e.Access,
		Weekly:     cloneSectionDeep(e.Weekly),
		Monthly:    cloneSectionDeep(e.Monthly),
		Account:    cloneSection(e.Account),
		PaidHealth: cloneSection(e.PaidHealth),
	}
}
func (e XAIExtras) mergeFrom(prev QuotaExtras) QuotaExtras {
	p, _ := prev.(XAIExtras)
	access := e.Access
	if access == "" {
		access = p.Access
	}
	return XAIExtras{
		Access:     access,
		Weekly:     mergeSection(p.Weekly, e.Weekly),
		Monthly:    mergeSection(p.Monthly, e.Monthly),
		Account:    mergeSection(p.Account, e.Account),
		PaidHealth: mergeSection(p.PaidHealth, e.PaidHealth),
	}
}

// DevinPlan is the plan name and dates from GetUserStatus.
type DevinPlan struct {
	Name  string  `json:"name"`
	Start Instant `json:"start"`
	End   Instant `json:"end"`
}

// DevinExtras holds the Devin fields outside the windows.
type DevinExtras struct {
	Plan Section[DevinPlan] `json:"plan"`
}

func (DevinExtras) Kind() ProviderKind { return ProviderDevin }
func (DevinExtras) isQuotaExtras()     {}
func (e DevinExtras) clone() QuotaExtras {
	return DevinExtras{Plan: cloneSection(e.Plan)}
}
func (e DevinExtras) mergeFrom(prev QuotaExtras) QuotaExtras {
	p, _ := prev.(DevinExtras)
	return DevinExtras{Plan: mergeSection(p.Plan, e.Plan)}
}

// PollTrigger says why a poll ran.
type PollTrigger int

const (
	PollBackground PollTrigger = iota
	PollOnDemand
)

func (t PollTrigger) String() string {
	if t == PollOnDemand {
		return "on_demand"
	}
	return "background"
}

// MarshalJSON writes the trigger name.
func (t PollTrigger) MarshalJSON() ([]byte, error) { return json.Marshal(t.String()) }

// UnmarshalJSON reads the trigger name.
func (t *PollTrigger) UnmarshalJSON(data []byte) error {
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	if name == "on_demand" {
		*t = PollOnDemand
	} else {
		*t = PollBackground
	}
	return nil
}

// PollStatus is the outcome of the last whole poll.
type PollStatus struct {
	At      time.Time   `json:"at"`
	Trigger PollTrigger `json:"trigger"`
	Error   string      `json:"error,omitempty"`
}

// QuotaSnapshot is the one typed quota record of a credential. apply is its
// only writer.
type QuotaSnapshot struct {
	Provider  ProviderKind
	Windows   []QuotaWindowObs
	Extras    QuotaExtras
	LastPoll  *PollStatus
	UpdatedAt time.Time
}

// QuotaObservation is what every source hands to apply. Sections lists the
// sub-requests the source is authoritative for. Header observations carry nil
// Sections, because headers are partial.
type QuotaObservation struct {
	Provider   ProviderKind
	Source     Source
	ObservedAt time.Time
	Windows    []QuotaWindowObs
	Sections   map[SectionID]SectionStatus
	Extras     QuotaExtras
	// Poll is set by the poll path so the whole-poll outcome lands with the data.
	Poll *PollStatus
}

// IsZero reports whether the snapshot has never been written.
func (s QuotaSnapshot) IsZero() bool {
	return s.Provider == "" && len(s.Windows) == 0 && s.Extras == nil && s.LastPoll == nil && s.UpdatedAt.IsZero()
}

// Clone returns an independent copy.
func (s QuotaSnapshot) Clone() QuotaSnapshot {
	out := s
	if s.Windows != nil {
		out.Windows = append([]QuotaWindowObs(nil), s.Windows...)
	}
	if s.Extras != nil {
		out.Extras = s.Extras.clone()
	}
	if s.LastPoll != nil {
		poll := *s.LastPoll
		out.LastPoll = &poll
	}
	return out
}

// Observe applies one observation. It is the only public writer of the snapshot.
func (s *QuotaSnapshot) Observe(obs QuotaObservation) bool {
	return s.apply(obs)
}

type quotaWindowKey struct {
	id    WindowID
	scope string
}

func (w QuotaWindowObs) key() quotaWindowKey { return quotaWindowKey{id: w.ID, scope: w.ModelScope} }

func (w QuotaWindowObs) equal(o QuotaWindowObs) bool {
	return w.ID == o.ID && w.Label == o.Label && w.ModelScope == o.ModelScope && w.Used == o.Used &&
		w.LimitReached == o.LimitReached && w.ResetAt.Equal(o.ResetAt) && w.Period == o.Period &&
		w.Section == o.Section && w.Source == o.Source && w.ObservedAt.Equal(o.ObservedAt)
}

// headerWindowRetention bounds how long a header-sourced window with an unknown
// period outlives its reset. Only a poll retires its own section's windows, so
// header windows need their own expiry or upstream-named Codex limits would
// accumulate without bound.
const headerWindowRetention = 8 * 24 * time.Hour

// expiredHeaderWindow reports whether a headers-section window reset more than
// one period (or the retention, when the period is unknown) before now.
func expiredHeaderWindow(w QuotaWindowObs, now time.Time) bool {
	if w.Section != SectionHeaders || w.ResetAt.IsZero() || now.IsZero() {
		return false
	}
	limit := w.Period
	if limit <= 0 {
		limit = headerWindowRetention
	}
	return now.Sub(w.ResetAt) > limit
}

// apply merges obs into the snapshot. Per window the newer ObservedAt wins and a
// poll beats headers on a tie. A section the observation reports as ok is
// authoritative: its stored windows that the observation did not return and
// that are not newer than the observation are dropped. A section reported as
// failed keeps its windows. Long-expired header windows are dropped. A provider
// change resets the snapshot first.
func (s *QuotaSnapshot) apply(obs QuotaObservation) bool {
	if s == nil || obs.Provider == "" {
		return false
	}
	changed := false
	if s.Provider != obs.Provider {
		*s = QuotaSnapshot{Provider: obs.Provider}
		changed = true
	}
	byKey := make(map[quotaWindowKey]QuotaWindowObs, len(s.Windows)+len(obs.Windows))
	for _, w := range s.Windows {
		byKey[w.key()] = w
	}
	observed := make(map[quotaWindowKey]struct{}, len(obs.Windows))
	for _, w := range obs.Windows {
		observed[w.key()] = struct{}{}
	}
	for section, status := range obs.Sections {
		if status != SectionOK {
			continue
		}
		for k, w := range byKey {
			if w.Section != section || w.ObservedAt.After(obs.ObservedAt) {
				continue
			}
			if _, ok := observed[k]; !ok {
				delete(byKey, k)
				changed = true
			}
		}
	}
	for k, w := range byKey {
		if expiredHeaderWindow(w, obs.ObservedAt) {
			delete(byKey, k)
			changed = true
		}
	}
	for _, w := range obs.Windows {
		if w.Source == "" {
			w.Source = obs.Source
		}
		if w.ObservedAt.IsZero() {
			w.ObservedAt = obs.ObservedAt
		}
		k := w.key()
		if old, ok := byKey[k]; ok {
			if old.ObservedAt.After(w.ObservedAt) {
				continue
			}
			if old.ObservedAt.Equal(w.ObservedAt) && old.Source == SourcePoll && w.Source != SourcePoll {
				continue
			}
			if old.equal(w) {
				continue
			}
		}
		if expiredHeaderWindow(w, obs.ObservedAt) {
			continue
		}
		byKey[k] = w
		changed = true
	}
	if obs.Extras != nil && obs.Extras.Kind() == s.Provider {
		if merged := obs.Extras.mergeFrom(s.Extras); !reflect.DeepEqual(s.Extras, merged) {
			s.Extras = merged
			changed = true
		}
	}
	if obs.Poll != nil {
		poll := *obs.Poll
		s.LastPoll = &poll
		changed = true
	}
	if !changed {
		return false
	}
	s.Windows = sortedQuotaWindows(s.Provider, byKey)
	if obs.ObservedAt.After(s.UpdatedAt) {
		s.UpdatedAt = obs.ObservedAt
	}
	for _, w := range s.Windows {
		if w.ObservedAt.After(s.UpdatedAt) {
			s.UpdatedAt = w.ObservedAt
		}
	}
	return true
}

// merge applies another snapshot of the same credential as a per-window
// observation, so the newest data of both survives. Restore and auth updates use it.
func (s *QuotaSnapshot) merge(other QuotaSnapshot) bool {
	if other.Provider == "" {
		return false
	}
	obs := QuotaObservation{
		Provider:   other.Provider,
		ObservedAt: other.UpdatedAt,
		Windows:    other.Windows,
		Extras:     other.Extras,
	}
	if other.LastPoll != nil && (s.LastPoll == nil || other.LastPoll.At.After(s.LastPoll.At)) {
		obs.Poll = other.LastPoll
	}
	return s.apply(obs)
}

func sortedQuotaWindows(provider ProviderKind, byKey map[quotaWindowKey]QuotaWindowObs) []QuotaWindowObs {
	windows := make([]QuotaWindowObs, 0, len(byKey))
	for _, w := range byKey {
		windows = append(windows, w)
	}
	order := quotaProviders[provider].classOrder
	rank := func(w QuotaWindowObs) int {
		class := w.Class(provider)
		for i, c := range order {
			if c == class {
				return i
			}
		}
		return len(order)
	}
	sort.SliceStable(windows, func(i, j int) bool {
		ri, rj := rank(windows[i]), rank(windows[j])
		if ri != rj {
			return ri < rj
		}
		if windows[i].ID != windows[j].ID {
			return windows[i].ID < windows[j].ID
		}
		return windows[i].ModelScope < windows[j].ModelScope
	})
	return windows
}

// ObserveResponseHeaders records the passive quota headers of one upstream
// response. The model decides the scope of model-scoped windows such as
// Claude's 7d_oi window.
func (s *QuotaSnapshot) ObserveResponseHeaders(provider, model string, h http.Header, now time.Time) bool {
	if s == nil || len(h) == 0 {
		return false
	}
	kind, ok := providerKindOf(provider)
	if !ok || quotaProviders[kind].fromHeaders == nil {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	obs, ok := quotaProviders[kind].fromHeaders(h, model, now)
	if !ok {
		return false
	}
	return s.apply(obs)
}

// RoutingWindows returns the windows that routing, cooldown clearing, and the
// forecast may act on. A window with an unknown percent that is not marked
// limit reached is skipped, and so is a window whose reset already passed.
func (s QuotaSnapshot) RoutingWindows(now time.Time) []QuotaWindow {
	if s.Provider == "" {
		return nil
	}
	var out []QuotaWindow
	for _, w := range s.Windows {
		class := w.Class(s.Provider)
		if class == "" {
			continue
		}
		used, known := w.Used.Get()
		if !known {
			if !w.LimitReached {
				continue
			}
			used = 100
		}
		if !w.ResetAt.IsZero() && !w.ResetAt.After(now) {
			continue
		}
		out = append(out, QuotaWindow{
			Class:       string(class),
			Provider:    string(s.Provider),
			ModelScope:  w.ModelScope,
			UsedPercent: used,
			ResetAt:     w.ResetAt,
			Window:      w.Period,
			ObservedAt:  w.ObservedAt,
		})
	}
	return out
}

/* ---------------------------------------------------------------- JSON */

type quotaWindowWire struct {
	ID            WindowID    `json:"id"`
	Class         WindowClass `json:"class"`
	Label         string      `json:"label,omitempty"`
	ModelScope    string      `json:"model_scope,omitempty"`
	Used          Percent     `json:"used_percent"`
	LimitReached  bool        `json:"limit_reached"`
	ResetAt       *string     `json:"reset_at"`
	PeriodSeconds *int64      `json:"period_seconds"`
	Section       SectionID   `json:"section"`
	Source        Source      `json:"source"`
	ObservedAt    *string     `json:"observed_at"`
}

type quotaSnapshotWire struct {
	Provider  ProviderKind      `json:"provider"`
	Windows   []quotaWindowWire `json:"windows"`
	Extras    json.RawMessage   `json:"extras"`
	LastPoll  *PollStatus       `json:"last_poll"`
	UpdatedAt *string           `json:"updated_at"`
}

type extrasKindProbe struct {
	Kind ProviderKind `json:"kind"`
}

type claudeExtrasWire struct {
	Kind ProviderKind `json:"kind"`
	ClaudeExtras
}

type codexExtrasWire struct {
	Kind ProviderKind `json:"kind"`
	CodexExtras
}

type xaiExtrasWire struct {
	Kind ProviderKind `json:"kind"`
	XAIExtras
}

type devinExtrasWire struct {
	Kind ProviderKind `json:"kind"`
	DevinExtras
}

func marshalQuotaExtras(extras QuotaExtras) (json.RawMessage, error) {
	switch e := extras.(type) {
	case nil:
		return json.RawMessage("null"), nil
	case ClaudeExtras:
		return json.Marshal(claudeExtrasWire{Kind: e.Kind(), ClaudeExtras: e})
	case CodexExtras:
		return json.Marshal(codexExtrasWire{Kind: e.Kind(), CodexExtras: e})
	case XAIExtras:
		return json.Marshal(xaiExtrasWire{Kind: e.Kind(), XAIExtras: e})
	case DevinExtras:
		return json.Marshal(devinExtrasWire{Kind: e.Kind(), DevinExtras: e})
	default:
		return nil, fmt.Errorf("quota extras: unknown kind %q", extras.Kind())
	}
}

func unmarshalQuotaExtras(data json.RawMessage) (QuotaExtras, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}
	var probe extrasKindProbe
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, err
	}
	switch probe.Kind {
	case ProviderClaude:
		var wire claudeExtrasWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, err
		}
		return wire.ClaudeExtras, nil
	case ProviderCodex:
		var wire codexExtrasWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, err
		}
		return wire.CodexExtras, nil
	case ProviderXAI:
		var wire xaiExtrasWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, err
		}
		return wire.XAIExtras, nil
	case ProviderDevin:
		var wire devinExtrasWire
		if err := json.Unmarshal(data, &wire); err != nil {
			return nil, err
		}
		return wire.DevinExtras, nil
	default:
		return nil, fmt.Errorf("quota extras: unknown kind %q", probe.Kind)
	}
}

// MarshalJSON adds the derived class to each window and the kind to the extras.
func (s QuotaSnapshot) MarshalJSON() ([]byte, error) {
	windows := make([]quotaWindowWire, 0, len(s.Windows))
	for _, w := range s.Windows {
		var period *int64
		if w.Period > 0 {
			seconds := int64(w.Period / time.Second)
			period = &seconds
		}
		windows = append(windows, quotaWindowWire{
			ID:            w.ID,
			Class:         w.Class(s.Provider),
			Label:         w.Label,
			ModelScope:    w.ModelScope,
			Used:          w.Used,
			LimitReached:  w.LimitReached,
			ResetAt:       jsonTime(w.ResetAt),
			PeriodSeconds: period,
			Section:       w.Section,
			Source:        w.Source,
			ObservedAt:    jsonTime(w.ObservedAt),
		})
	}
	extras, err := marshalQuotaExtras(s.Extras)
	if err != nil {
		return nil, err
	}
	return json.Marshal(quotaSnapshotWire{
		Provider:  s.Provider,
		Windows:   windows,
		Extras:    extras,
		LastPoll:  s.LastPoll,
		UpdatedAt: jsonTime(s.UpdatedAt),
	})
}

// UnmarshalJSON is the inverse of MarshalJSON. The class is recomputed, never read.
func (s *QuotaSnapshot) UnmarshalJSON(data []byte) error {
	var wire quotaSnapshotWire
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	out := QuotaSnapshot{Provider: wire.Provider, LastPoll: wire.LastPoll}
	for _, w := range wire.Windows {
		resetAt, err := parseJSONTime(w.ResetAt)
		if err != nil {
			return err
		}
		observedAt, err := parseJSONTime(w.ObservedAt)
		if err != nil {
			return err
		}
		var period time.Duration
		if w.PeriodSeconds != nil && *w.PeriodSeconds > 0 {
			period = time.Duration(*w.PeriodSeconds) * time.Second
		}
		out.Windows = append(out.Windows, QuotaWindowObs{
			ID:           w.ID,
			Label:        w.Label,
			ModelScope:   w.ModelScope,
			Used:         w.Used,
			LimitReached: w.LimitReached,
			ResetAt:      resetAt,
			Period:       period,
			Section:      w.Section,
			Source:       w.Source,
			ObservedAt:   observedAt,
		})
	}
	extras, err := unmarshalQuotaExtras(wire.Extras)
	if err != nil {
		return err
	}
	out.Extras = extras
	updatedAt, err := parseJSONTime(wire.UpdatedAt)
	if err != nil {
		return err
	}
	out.UpdatedAt = updatedAt
	*s = out
	return nil
}

func jsonTime(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	formatted := t.UTC().Format(time.RFC3339Nano)
	return &formatted
}

func parseJSONTime(value *string) (time.Time, error) {
	if value == nil || strings.TrimSpace(*value) == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, *value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed, nil
}
