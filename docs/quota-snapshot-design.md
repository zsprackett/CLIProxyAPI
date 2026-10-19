# Sketch (typed snapshot): one QuotaSnapshot per credential, shared windows + per-provider extras

## 1. Shape in one paragraph
Every source (background poll, on-demand poll, Devin refresh, passive response headers, Codex websocket events turned into headers) produces one `QuotaObservation`. `QuotaSnapshot.apply(obs)` is the only writer. The snapshot holds `Windows []QuotaWindowObs` (the only part that routing, cooldown clearing and the forecast read) and `Extras` (a sealed sum type, one struct per provider, JSON `kind` discriminator). A window's pool class is never stored: it is computed from `(provider, window ID, period)` by one function per provider, so two parsers cannot disagree. Per-model snapshots go away: a window carries its own `ModelScope`, and there is one snapshot per credential.

## 2. Go data model (package `sdk/cliproxy/auth`, new file `quota_snapshot.go`)
```go
type ProviderKind string // "claude" | "codex" | "xai" | "devin"

// Percent is a nullable 0-100 value. The zero value is unknown and marshals as null.
type Percent struct{ v float64; ok bool }
func KnownPercent(v float64) Percent          { panic("not implemented") } // clamps 0-100
func (p Percent) Get() (float64, bool)         { panic("not implemented") }
func (p Percent) MarshalJSON() ([]byte, error) { panic("not implemented") } // null when !ok

type WindowID string     // stable per provider: "five_hour", "seven_day_fable", "primary", "code_review.secondary", "additional.<key>.primary", "weekly", "daily"
type SectionID string    // which upstream sub-request owns a window or an extras field: "claude.usage", "codex.usage", "xai.weekly", "xai.monthly", "devin.status", "headers"
type Source string       // "poll" | "headers" | "refresh"

type QuotaWindowObs struct {
	ID           WindowID
	Label        string        // upstream-named windows only (Codex additional limit_name); UI maps known IDs to i18n
	ModelScope   string        // "" = credential-wide; model family ("fable") for scoped windows
	Used         Percent       // unknown stays unknown; never 0 by default
	LimitReached bool          // upstream said exhausted, even when Used is unknown (Codex limit_reached)
	ResetAt      time.Time     // zero = unknown
	Period       time.Duration // zero = unknown
	Section      SectionID
	Source       Source
	ObservedAt   time.Time
}
// Class is derived, never stored: quotaProviders[p].classOf(id, period). "" = display-only window.
func (w QuotaWindowObs) Class(p ProviderKind) WindowClass { panic("not implemented") }

// QuotaExtras is sealed: only the four structs below implement it.
type QuotaExtras interface {
	Kind() ProviderKind
	mergeFrom(prev QuotaExtras) QuotaExtras // per-field mergeSection; prev of another kind is dropped
	isQuotaExtras()
}

type SectionStatus string // "ok" | "failed" | "absent" (never fetched) | "not_applicable" (e.g. paid probe on an OAuth account)

// Section is one sub-request's result. Value is the last good value: it survives a later
// failure (Status=failed, Error set, ValueAt older than CheckedAt) so the UI shows "stale since",
// never a substitute from a different payload.
type Section[T any] struct {
	Status    SectionStatus
	Value     *T
	ValueAt   time.Time
	CheckedAt time.Time
	Error     string // bounded, no secrets
}

type ClaudeExtras struct {
	Plan       Section[string]           // "plan_team"|"plan_max"|"plan_pro"|"plan_free", from /api/oauth/profile
	ExtraUsage Section[ClaudeExtraUsage]
}
type ClaudeExtraUsage struct{ Enabled bool; MonthlyLimit, UsedCredits float64; Utilization Percent }

type CodexExtras struct {
	Plan         Section[string]          // usage payload plan_type or X-Codex-Plan-Type
	Access       Section[CodexAccess]     // allowed / limit_reached of the main rate limit
	Credits      Section[CodexCredits]
	Subscription Section[CodexSubscription] // /subscriptions?account_id=
	ResetCredits Section[CodexResetCredits] // /wham/rate-limit-reset-credits
}
type CodexAccess struct{ Allowed, LimitReached *bool }
type CodexCredits struct{ HasCredits, Unlimited *bool; Balance *string }
type CodexSubscription struct{ ActiveUntil time.Time }
type CodexResetCredits struct{ Available, ApplicableAvailable *int; Credits []CodexResetCredit }
type CodexResetCredit struct{ ID, Status string; GrantedAt, ExpiresAt time.Time }

type XAIAccess string // "grok_cli" (billing endpoints) | "api_key" (paid health probe); decided once by xaiUsingAPI(auth)
type XAIExtras struct {
	Access     XAIAccess
	Weekly     Section[XAIWeekly]     // product usage breakdown, period; the percent lives in window "weekly"
	Monthly    Section[XAIMonthly]    // cents; the percent lives in window "monthly"
	Account    Section[XAIAccount]    // /v1/user?include=subscription + /v1/settings
	PaidHealth Section[XAIPaidHealth] // api_key only; fetched on on-demand polls only (costs a token)
}
type XAIWeekly struct{ PeriodStart, PeriodEnd time.Time; ProductUsage []XAIProductUsage }
type XAIProductUsage struct{ Product string; Used Percent }
type XAIMonthly struct {
	LimitCents, UsedCents, IncludedUsedCents, OnDemandCapCents, OnDemandUsedCents, PrepaidBalanceCents *float64
	PeriodStart, PeriodEnd time.Time
}
type XAIAccount struct{ PlanLabel, Tier string }
type XAIPaidHealth struct{ UserID, TeamID string; ChatOK bool; Source string }

type DevinExtras struct{ Plan Section[DevinPlan] }
type DevinPlan struct{ Name string; Start, End time.Time }

type PollTrigger int // PollBackground | PollOnDemand
type PollStatus struct{ At time.Time; Trigger PollTrigger; Error string } // last whole-poll outcome

type QuotaSnapshot struct {
	Provider  ProviderKind
	Windows   []QuotaWindowObs // sorted by provider classOrder then ID
	Extras    QuotaExtras      // nil until the first observation; kind == Provider (checked in apply)
	LastPoll  *PollStatus
	UpdatedAt time.Time
}
func (s QuotaSnapshot) MarshalJSON() ([]byte, error)  { panic("not implemented") } // adds "class" per window, "kind" in extras
func (s *QuotaSnapshot) UnmarshalJSON(b []byte) error  { panic("not implemented") } // kind -> quotaProviders[kind].newExtras()

// QuotaObservation is what every source hands to apply. Sections lists the sub-requests the
// source is authoritative for, with their outcome. Header observations carry Sections = nil.
type QuotaObservation struct {
	Provider   ProviderKind
	Source     Source
	ObservedAt time.Time
	Windows    []QuotaWindowObs
	Sections   map[SectionID]SectionStatus
	Extras     QuotaExtras // partial: Section fields with Status "absent" mean "not in this observation"
}
func (s *QuotaSnapshot) apply(obs QuotaObservation) bool { panic("not implemented") } // see section 5
```

`Auth` gets `QuotaSnapshot QuotaSnapshot \`json:"quota_snapshot"\``. `QuotaState` loses `ObservedAt` and `Signals` and keeps cooldown fields only, so `ModelState.Quota` cannot hold a snapshot (the type has nowhere to put one). `cooldownFieldsOf` / `applyCooldownFields` / `ClearObservationSignals` / `mergeQuotaObservation` are deleted.

Call sites:
```go
// 1. on demand and background share this
func (m *Manager) PollQuotaNow(ctx context.Context, authID string) (QuotaSnapshot, error) { panic("not implemented") }
// 2. routing view, unchanged name; skips windows whose Used is unknown and !LimitReached,
//    or whose ResetAt has passed; LimitReached with unknown Used reports 100.
type QuotaWindow struct{ Class, Provider, ModelScope string; UsedPercent float64; ResetAt time.Time; Window time.Duration; ObservedAt time.Time }
func QuotaWindowsForAuth(auth *Auth) []QuotaWindow { panic("not implemented") }
// 3. model is needed so Claude's 7d_oi header window gets a ModelScope instead of being dropped
func (s *QuotaSnapshot) ObserveResponseHeaders(provider, model string, h http.Header, now time.Time) bool { panic("not implemented") }

type QuotaPoller interface {
	// ErrQuotaNotPollable replaces today's (nil, nil) for API keys / setup tokens.
	// A non-nil error means no section succeeded; per-section failures live in obs.Sections.
	PollQuota(ctx context.Context, auth *Auth, trigger PollTrigger) (QuotaObservation, error)
}
```

## 3. Provider registry (adding a provider = adding a row and a variant)
```go
type quotaProvider struct {
	newExtras    func() QuotaExtras
	fromHeaders  func(h http.Header, model string, now time.Time) (QuotaObservation, bool) // nil = no passive signal
	classOf      func(id WindowID, period time.Duration) WindowClass
	classOrder   []WindowClass
	showsCapacity func(obs QuotaObservation) bool // cooldown clear; evaluated on the poll result, not the merged snapshot
	scopeOf      func(model string) string        // model -> ModelScope family; nil = provider has no scoped windows
}
var quotaProviders = map[ProviderKind]quotaProvider{ "claude": {...}, "codex": {...}, "xai": {...}, "devin": {...} }
```
`ProviderSupportsQuotaObservation`, `quotaWindowClassOrder`, the provider `switch` in `quotaWindowsFromSnapshot`, `pollShowsQuotaCapacity` and `weeklyQuotaResetFromSnapshot` all become lookups in this table. A new provider adds: one row, one `XxxExtras` struct, one poll parser in `helps`, one UI body component, one TS union member. The TS adapter map is `{[K in QuotaExtras['kind']]: Adapter<K>}`, so a missing body is a compile error.

Class rules (the only copies):
- claude: five_hour→`claude:5h`; seven_day→`claude:7d`; seven_day_fable→`claude:7d_oi`; oauth_apps/opus/sonnet/cowork→"" (display only).
- codex: `primary`/`secondary` by period: >10080m `codex:monthly`, =10080m `codex:weekly`, else `codex:5h`; code_review.* and additional.* → "".
- xai: weekly→`xai:weekly`, monthly→`xai:monthly`. devin: daily→`devin:daily`, weekly→`devin:weekly`.

## 4. Module boundaries
| Concern | Owner | Notes |
|---|---|---|
| Fetch poll payloads | `internal/runtime/executor/{claude,codex,xai,devin}_executor_quota.go` | Claude: usage + profile. Codex: usage + subscriptions + reset-credits. xAI: weekly + monthly + user/settings (grok_cli), or /v1/me + 1-token ping (api_key, on demand only). Devin: `FetchUserStatus`. Sub-requests run independently; each one sets its own SectionStatus. |
| Parse poll payloads | `internal/runtime/executor/helps/quota_{claude,codex,xai,devin}.go` | Return `cliproxyauth.QuotaObservation`; build windows only through `cliproxyauth.NewQuotaWindow(...)`. `ClaudeUsageQuotaHeaders`, `CodexUsageQuotaHeaders`, `XAIBillingQuotaHeaders` deleted. |
| Parse passive headers | `sdk/cliproxy/auth/quota_headers_{claude,codex}.go` | The `fromHeaders` rows. Codex websocket keeps `helps.ParseCodexQuotaEventHeaders` → headers → this parser (one parser for both transports). Bounds and control-character checks from `collectQuotaSignals` move here. |
| Store + merge | `sdk/cliproxy/auth/quota_snapshot.go` | `apply`, `mergeSection`, JSON. Only writer of `Auth.QuotaSnapshot`. |
| Poll orchestration | `sdk/cliproxy/auth/quota_poll.go` | `runQuotaPoll` and `PollQuotaNow` both call `m.pollQuota(ctx, id, trigger)`; a per-auth singleflight (manager field, replaces the loop-local `inFlight`) makes a click during a background poll wait for that poll. `lastPolled` moves to the manager so a click also resets the background timer. |
| Devin refresh | `devin_executor.go:202-223` | Stops writing `Quota.Signals`; returns the `helps.DevinQuotaObservation(status)` through the manager so it goes through `apply` (Source "refresh"). Same parser as `DevinExecutor.PollQuota`. |
| Serve | `management/auth_files.go` | listing: `entry["quota_snapshot"] = auth.QuotaSnapshot`; `quota` and `model_quotas` deleted. New `POST /v8/management/credentials/quota/poll {auth_index}` → `{"quota_snapshot": ...}`; 404 unknown, 422 `ErrQuotaNotPollable`, 200 with `last_poll.error` set when the upstream failed. |
| Consumers | `selector_soonest_reset.go`, `quotaforecast`, `quota_poll_clear.go`, `conductor_cooldown.go` | All read `QuotaWindowsForAuth` (selector filters `ModelScope == "" or == scopeOf(model)`). Forecast now sees the polled Fable window. |

UI (`Cli-Proxy-API-Management-Center`):
- Add `src/features/quota/snapshot/{types.ts,api.ts,WindowList.tsx}`: TS types, `pollCredentialQuota(authIndex)`, one shared window renderer (bar, unknown, limit reached, stale badge from `observed_at`).
- Each of `providers/{claude,codex,xai,devin}/*QuotaBody.tsx` takes `{ snapshot, extras: Extract<QuotaExtras,{kind:K}> }` and renders extras only. Codex "use a reset credit" stays an api-call write; on success it calls `pollCredentialQuota`.
- `ledgerModel.ts`, `quotaTimelineModel.ts`, `resetSchedule.ts`, `AuthFileQuotaSection.tsx` read `file.quota_snapshot` (one source; on-demand result overrides until the next 60s list refresh carries a newer `updated_at`).
- Delete: `providers/{claude,codex,xai,devin}/data.ts`, `devin/requests.ts`, `devin/useDevinQuotaAutoLoad.ts`, the claude/codex/xai/devin parts of `utils/quota/{builders,parsers,constants}.ts`, `utils/quota/xaiPaid.ts`, ledger observed readers (220-415) for these providers, store slices `claudeQuota/codexQuota/xaiQuota/devinQuota` (one `quotaSnapshots` slice keyed by auth index).

## 5. State model: merging poll and passive observation
- Join key: window `(ID, ModelScope)`; extras field by Go field (section). Snapshot by credential (auth ID). No per-model snapshot.
- Conflict winner, windows: the observation with the newer `ObservedAt` per key. Tie: poll beats headers (it also carries extras).
- Conflict winner, extras: `mergeSection(prev, next)`: next `absent` → keep prev. next `ok` and `ValueAt >= prev.ValueAt` → next. next `failed` → keep `prev.Value`/`ValueAt`, set Status failed, Error, CheckedAt. next `not_applicable` → clear Value.
- Order rule: per key, by `ObservedAt`, not by arrival. A poll that started before a passive response and finished after it loses on the windows the response carried. `ObservedAt` of a poll = time the response body was read.
- Unknown id, stored but not in the observation: headers observation → keep (headers are partial). Poll with `Sections[s] == ok` → drop windows of section s that the poll did not return (the poll is authoritative for its section). Poll with `Sections[s] == failed` → keep section s windows untouched.
- Unknown id, in the observation but not stored: add.
- Expiry instead of replacement: today's "replace the whole map" existed so a stale `limit reached` could not linger. Here each window expires on its own: `QuotaWindowsForAuth` drops a window once `ResetAt` passes, and the UI shows it as "reset, awaiting data".
- Provider change (`obs.Provider != s.Provider`): reset the snapshot, then apply.
- Cooldown clear: `showsCapacity(obs)` on the poll observation alone, then the existing baseline guard. claude: windows five_hour and seven_day both present with known Used < 100. codex: every classed window known < 100, Access ok and !LimitReached and Allowed != false.

Pseudocode for `apply`:
```
if obs.Provider != s.Provider: s = QuotaSnapshot{Provider: obs.Provider}
byKey = index s.Windows by (ID, ModelScope)
for sec, st in obs.Sections where st == ok: delete keys in byKey with Section == sec and not in obs.Windows
for w in obs.Windows: if old, ok = byKey[k]; ok and (old.ObservedAt > w.ObservedAt or (== and old.Source == poll)): continue; byKey[k] = w
s.Extras = obs.Extras.mergeFrom(s.Extras)   // nil-safe; kind checked
s.Windows = sort(byKey, classOrder, ID); s.UpdatedAt = max(...); return changed
```

## 6. Unknown values (the known disagreements, each made unrepresentable)
- Null Claude utilization: `Percent{}` → JSON null → UI "unknown". `QuotaWindowsForAuth` skips it, so routing never reads 0.
- xAI weekly 503: section `xai.weekly` = failed. The weekly window and `Weekly` value keep the last good data with old `observed_at`; the UI shows "stale since". The monthly payload only ever produces window `monthly`; no code path maps it to `weekly`.
- Codex classification: one `classOf` row used by the poll parser, the header parser and the websocket path.
- Codex limit_reached with no percent: `LimitReached: true, Used: null`. UI shows "limit reached"; routing reads 100.
- Fable: poll emits `seven_day_fable` with `ModelScope: "fable"`, class `claude:7d_oi`, at the credential level; the forecast reads it. `iguana_necktie` is only a fallback inside the Claude parser.
- xAI paid detection: `XAIExtras.Access` set by the backend `xaiUsingAPI`; the UI switches on it.
- Devin: the UI reads the same snapshot the backend writes.

## 7. JSON and TS (derived, not hand-written twice)
```ts
type SectionStatus = 'ok' | 'failed' | 'absent' | 'not_applicable';
type Section<T> = { status: SectionStatus; value: T | null; value_at: string | null; checked_at: string | null; error?: string };
interface QuotaWindow { id: string; class: string; label?: string; model_scope?: string; used_percent: number | null;
  limit_reached: boolean; reset_at: string | null; period_seconds: number | null; section: string; source: 'poll'|'headers'|'refresh'; observed_at: string }
type QuotaExtras =
  | { kind: 'claude'; plan: Section<string>; extra_usage: Section<ClaudeExtraUsage> }
  | { kind: 'codex'; plan: Section<string>; access: Section<CodexAccess>; credits: Section<CodexCredits>; subscription: Section<{active_until: string}>; reset_credits: Section<CodexResetCredits> }
  | { kind: 'xai'; access: 'grok_cli' | 'api_key'; weekly: Section<XaiWeekly>; monthly: Section<XaiMonthly>; account: Section<XaiAccount>; paid_health: Section<XaiPaidHealth> }
  | { kind: 'devin'; plan: Section<{ name: string; start: string | null; end: string | null }> };
interface QuotaSnapshot { provider: QuotaExtras['kind']; windows: QuotaWindow[]; extras: QuotaExtras | null;
  last_poll: { at: string; trigger: 'background' | 'on_demand'; error?: string } | null; updated_at: string | null }
```
A Go test marshals one fixture per provider and the UI test suite type-checks the same fixtures (`tests/fixtures/quota_snapshot_*.json`), so the two sides cannot drift silently.

## 8. Persistence and migration
- `CooldownStateRecord` gains `Snapshot *QuotaSnapshot \`json:"snapshot,omitempty"\`` on the auth-level record (`Model == ""`) only; `withQuotaObservations` attaches it, throttled by `quotaObservationPersistInterval` as today. Restore applies it as an observation per window (newest wins), so live data after startup is kept. Windows older than `quotaObservationRetention` (8d) are pruned on restore; extras are kept (plan, credits).
- Migration (one release, then delete): restore decodes legacy `quota.signals` through a decode-only struct. Auth-level legacy signals go through `quotaProviders[p].fromHeaders` (they are canonical header names); Devin's legacy keys through a small `devinLegacySignals` adapter; model-scoped legacy records feed `fromHeaders` with `model = record.Model`, so 7d_oi becomes a scoped window. Legacy data is `Source: headers`, so the first poll overrides it.
- `Auth.QuotaSnapshot` is not written to auth files by `m.persist` (`json:"-"` on the auth file path, or excluded the way `Quota` is today); question 4.

## 9. Typed snapshot vs a generic metrics/groups contract
Better: each provider's extras are Go and TS types, so the compiler catches a renamed field in both repos, and the UI can lay out each provider as it does today (credits panel, reset-credit list, xAI cents table) without stringly keys. Unknown and stale are typed (`Percent`, `Section`), not conventions on a float. Merge rules can be per field (Codex credits from headers, xAI weekly vs monthly) instead of one rule for an untyped bag.
Worse: a new provider needs a Go struct, a TS union member and a body component, where a generic contract needs only a parser; the UI has four bodies to keep; the plugin quota contract stays a second shape for plugin providers (two shapes on one page); the JSON contract is wider, so a field change touches both repos.

## 10. Rejected
Keep `Signals map[string]string` as the store and add a server-side `QuotaSnapshotFor(auth)` that parses it on read. It is the smallest diff, but the map cannot hold extras that only the poll fetches (profile plan, subscription, reset credits, xAI account) without inventing header names, and the "replace the whole map" rule still erases poll-only data on every passive response, which breaks usage 3.

## 11. Limits
- antigravity, kimi, meta, and Claude reset grants (`ClaudeResetGrants.tsx`, its own service) are untouched.
- No history in the snapshot; the forecast keeps its own samples.
- Home mode: the loop still does not run there; `PollQuotaNow` returns `ErrHomeMode` (question 3).
- With `quota-poll-interval-seconds: 0`, Claude, Codex and xAI extras only appear after a click; Devin also gets data on token refresh.
- The xAI paid probe never runs in the background.

## 12. Questions (each can change the shape)
1. Does an `anthropic-ratelimit-unified-7d_oi-*` header on a non-Fable response describe Fable, or that response model's own scoped limit? This decides `scopeOf` and whether the join key needs `ModelScope` at all.
2. Codex additional limits: HTTP headers use a short name (`x-codex-bengalfox-*`), websocket and `/wham/usage` use `limit_name` (and `/wham/usage` has `metered_feature`). Is `metered_feature` the HTTP short name? If not, the poll and headers produce two IDs for one limit, and the poll drops the header copy every 5 minutes (flicker).
3. Should `PollQuotaNow` work in Home mode (forwarded to Home), or refuse?
4. Is `Auth.Quota` written to the auth file today through `m.persist`? If yes, the snapshot needs the same exclusion, or it leaks into credential files.
5. Does CLIProxyAPIHome read `quota.signals` / `model_quotas` from the listing? If yes, deleting them needs a Home change.
6. Should the on-demand poll also bypass a recent background result (always hit upstream), or return the snapshot if it is younger than N seconds?

## 13. Decisions (approved by Zac, 2026-10-19)
- Typed snapshot (this sketch) selected over the generic plugin-contract sketch. `sdk/pluginapi` is not changed.
- Scope: claude, codex, xai, devin. Antigravity, kimi, meta stay UI-parsed.
- The model carries everything the quota page shows today.
- Divergence from upstream router-for-me is accepted for these files and the UI quota page.
- Background poll interval stays config-driven.
- Home mode: `PollQuotaNow` returns `ErrHomeMode`; the endpoint answers 409.
- An on-demand poll always calls upstream, even right after a background poll.
- Legacy `Signals` are converted once on restore, then the legacy decoder is deleted after one release.
- Open before the plan: question 1 (7d_oi scope), question 2 (Codex additional-limit ids), the xAI paid-credential rule. Question 4 is answered: credential files on disk carry no `quota` key, so the snapshot is not written into them. Question 5 (CLIProxyAPIHome) cannot be checked locally; the plan lists it as a risk.
