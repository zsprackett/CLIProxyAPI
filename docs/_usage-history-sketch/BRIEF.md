# Brief: usage history (SQLite) + cost + Usage page

Repo: /home/zsprackett/.t3/worktrees/CLIProxyAPI/t3-83c8ccb1 (Go 1.26 proxy, module github.com/router-for-me/CLIProxyAPI/v8; read AGENTS.md). Panel: web/management-center (React 19 + Vite, bun:test; read its AGENTS.md).

## Outcome (one sentence)
Zac can see up to a year of usage, cache use and estimated API-price cost in the Management Center, sliced by model, client API key, provider, credential and agent, by hour, day or month, and export it as CSV.

## Decisions already made by Zac (do not reopen)
- Built in, not a plugin. Inspired by https://github.com/Willxup/cpa-usage-keeper (items: long history, full token breakdown, cost, Usage page). No backups, no per-key public view, no OIDC.
- Storage: embedded SQLite via `modernc.org/sqlite` (pure Go; release builds include CGO_ENABLED=0 static linux artifacts, so cgo drivers are out). New dependency approved.
- Retention: ROLLUPS ONLY. Hourly rollups kept for `retention-days`, default 365, configurable (raise later keeps more; lower deletes older rows at the next cleanup). Per-request rows stay in the existing in-memory requestlog (7-30 days); do NOT store per-request rows in SQLite.
- Prices: built-in trimmed snapshot of LiteLLM `model_prices_and_context_window.json` (go:embed, refreshed by a repo script) + a DAILY fetch of the LiteLLM file at runtime (with a fetch timeout; this needs a new named exception in the AGENTS.md timeouts list) + per-model overrides in config.yaml. Cost is computed at READ time from current prices (a price change re-prices all history). Models with no price are reported as unpriced; their tokens still count. Cost is labelled as an API-price estimate (most traffic is subscription OAuth).
- Split Claude cache writes into 5-minute and 1-hour buckets (1h writes cost 1.6x of 5m: opus-5-5 5m $5/M vs 1h $8/M). Today TokenBreakdown has one `Input.CacheWriteTokens`. Anthropic usage JSON carries `cache_creation.ephemeral_5m_input_tokens` / `ephemeral_1h_input_tokens`. This fork turns on 1h caching for main conversations.

## Measured on Zac's live deploy (last 50k requests, 2026-10-21..22)
~22-28k requests/day; ~40-50 distinct hourly rollup keys/day (hour, provider, auth, model, keyHash, agent); 6-7 daily keys. Models: claude-opus-5-5 (51%), claude-sonnet-5 (44%), grok-4.7 (3%), claude-sonnet-5-5 (2%). LiteLLM covers all four (keys `claude-opus-5-5`, `claude-sonnet-5`, `claude-sonnet-5-5`, `xai/grok-4.7`; fields input_cost_per_token, output_cost_per_token, cache_read_input_token_cost, cache_creation_input_token_cost, cache_creation_input_token_cost_above_1hr). A year of hourly rollups is ~18k rows: tiny. Design for 100x that without trouble.

## Facts about the code
Usage pipeline (sdk/cliproxy/usage/manager.go, accounting.go):
- Every upstream attempt publishes exactly one `coreusage.Record` (retries = several records, same TraceID). One global Manager, ONE serial worker goroutine, unbounded queue, calls every plugin's `HandleUsage(ctx, Record)` in registration order. A plugin MUST NOT do I/O in HandleUsage (it stalls all other plugins). Existing plugins aggregate in memory and flush on a 1-minute tick.
- Record fields that matter: Provider (executor id: claude, codex, xai, gemini, vertex, aistudio, antigravity, kimi, devin, meta, openai-compatible-<name>), Model (upstream model, alias resolved, thinking suffix stripped), Alias (what the client sent), AuthID, APIKey (RAW client key: hash it with `quotaforecast.HashClientKey`, 12 hex of sha256, "-" when empty), RequestedAt (attempt start), Latency, TTFT, Failed, Fail.StatusCode, Detail.
- Agent is not on Record: derive with `quotaforecast.AgentFamily(internallogging.GetClientRequestMetadata(ctx).UserAgent)` inside HandleUsage (ctx may be cancelled; values are still readable).
- Tokens: use `Detail.TokenBreakdown` when `.Valid()`: Input{Total, Uncached, CacheRead, CacheWrite}, Output{Total, NonReasoning, Reasoning} (reasoning is inside Output.Total), Quality complete|inconsistent|unclassified, UnclassifiedTokens. If not valid, call `usage.EnsureTokenBreakdownForProvider(detail, provider, executorType)`. Do NOT use Detail.CachedTokens (Claude copies cache-creation into it). Unclassified providers (devin, unknown) have only a total.
- Gating: `usage-statistics-enabled` only gates the redis queue plugin; other plugins have their own `enabled` config.
- Claude usage parsing: internal/runtime/executor/helps/usage_helpers.go (parseClaudeUsageNode ~L1178, ParseClaudeStreamUsage, MergeStreamUsageDetail).

Existing sibling services (follow their shape unless you have a reason):
- internal/requestlog (Service singleton `Default()`, Start(ctx, manager)/Stop(ctx), 1-min tick, `refreshEnabled(manager.RuntimeConfig())` pulled on every tick AND from HTTP query goroutines (so open logic must be idempotent and locked), snapshot `<auth-dir>/.request-history.gz`, disabled reasons `disabled|home_mode|unavailable` returned as `{enabled:false, reason}` with HTTP 200).
- internal/quotaforecast: same shape; also keeps HOURLY usage buckets keyed (Hour, AuthID, Model, KeyHash, Agent) with only {Requests, Tokens = total - cacheRead}, providers claude/codex/xai/devin only, retention 14 days (max 60), in `.quota-forecast.gz`. Its attribution (attribution.go `attribute(members, usage []UsageBucket, ...)`) regresses quota burn on these buckets. This is a SECOND hourly usage aggregation. Policy "one path": decide whether quotaforecast should read its buckets from the new store and drop its own (and say what happens when usage history is disabled), or justify keeping both.
- Config: internal/config/config_types.go (QuotaForecastConfig, RequestHistoryConfig ~L352-380), fields on Config (config.go ~L92). v8 key paths in internal/config/config_v8.go `buildV8Paths` (request-history lives at `observability.usage.request-history`). Also config_diff.go log lines and config.example.yaml docs. Both siblings default to disabled.
- Auth dir: `util.ResolveAuthDir(cfg.AuthDir)`; files start with "." and are not *.json so the auth watcher ignores them. New file: `<auth-dir>/.usage-history.db` (+ -wal/-shm). Unverified: git-store/object-store modes may mirror the auth dir remotely.
- Lifecycle: sdk/cliproxy/service_lifecycle.go. Run registers `usage.RegisterNamedPlugin("quota-forecast", ...)`/`("request-log", ...)` and Starts them at ~L100-103 in the non-home branch. Shutdown (~L238-367) stops those services at step 4 but `usage.StopDefault()` runs LAST, and Manager.Stop does not wait for the worker: queued records are lost after the final flush. The new store must persist records drained at shutdown (fix the order or make StopDefault wait).
- HTTP: internal/api/server_management_v8.go, `v8 := engine.Group("/v8/management")` with management auth middleware; existing GETs `/observability/requests`, `/observability/requests/sessions`, `/observability/quota/forecast`; handlers in internal/api/handlers/management/{request_history,quota_forecast}.go use package-level injectable service vars for tests. `/observability/usage/queue` (destructive pop) and `/observability/usage/api-keys` (upstream key counters) already exist: do not collide. Never touch /v0/management.
- Model registry/remote updater: internal/registry/model_updater.go fetches models.json every 3h with a 30s timeout and two URLs; a precedent for a remote catalog fetch. Pricing exists nowhere in the repo.
- Model identity: price lookup on Record.Model. LiteLLM keys are bare (`claude-opus-5-5`) or provider-prefixed (`xai/grok-4.7`, `vertex_ai/...`). Devin ids use dashes/`devin/` prefix and antigravity uses tweaked ids (e.g. claude-opus-4-6-thinking): these need overrides or stay unpriced.
- Conventions: gofmt; no log.Fatal; logrus; wrap defer Close errors; injectable `now func() time.Time` clocks, no wall-clock sleeps in tests; internal/runtime/executor holds executors only (helpers go in helps/); avoid standalone internal/translator changes. Windows is a release target.

Panel (web/management-center/src):
- Routes: router/MainRoutes.tsx flat table; sidebar in components/layout/MainLayout.tsx `navGroups` group `observe` (quota, requests, logs). Hash router: new page `#/usage`.
- API: services/api/client.ts `apiClient.get(path, {params})` (base `/v8/management`, bearer key); domain modules (requestLog.ts, quotaForecast.ts) parse `unknown` with pure `normalizeXxx` into typed camelCase and never throw; re-export from services/api/index.ts.
- Hooks: plain useState hooks with `getConnectionRevision()` stale guards and `useInterval` (see features/requests/useRequestFeed.ts, features/quota/forecast/useQuotaForecast.ts).
- NO chart library: charts are hand-drawn SVG with geometry in pure `*Model.ts` functions (features/quota/forecast/ForecastChart.tsx + forecastModel.ts, dashboard ThroughputChart). No categorical palette exists; theme tokens in styles/themes.scss (light/white/dark blocks). Tables: components/ui/Table. CSV: none exists; utils/download.ts `downloadBlob`.
- Client key labels: `clientKeyHash` (forecastModel.ts) + features/config/apiKeyNames.ts `readApiKeyNames`; raw keys never render.
- Tests: bun:test, no DOM. Views are prop-driven and tested with renderToStaticMarkup; models and normalizers have fixture tests; each feature has its own locale parity test (tests/requestLogLocales.test.ts). Locales: en, zh-CN, zh-TW, ru (fallback zh-CN).

## Usage (write types from these; change types, not usage, if they disagree)

1. Lifecycle wiring, sdk/cliproxy/service_lifecycle.go:
```go
// Run, non-home branch, next to quota-forecast and request-log:
usage.RegisterNamedPlugin("usage-history", usagehistory.Default())
usagehistory.Default().Start(ctx, s.coreManager)

// Shutdown: records still queued at shutdown must reach the store before its last write.
usage.StopDefault()                 // drains the queue
usagehistory.Default().Stop(ctx)    // final flush, close the DB
```

2. Management handler, internal/api/handlers/management/usage_history.go:
```go
// GET /v8/management/observability/usage/history
//   ?from=2026-01-01&to=2026-10-22&tz=America/Toronto&bucket=day&group=model
//   &provider=claude&model=claude-opus-5-5&key=cdd37df063c0&auth=...&agent=claude-cli
func (h *Handler) GetUsageHistory(c *gin.Context) {
	q, err := usagehistory.ParseQuery(c.Request.URL.Query()) // boundary: raw strings -> typed query
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, usageHistoryService().Report(c.Request.Context(), q))
}

// GET /v8/management/observability/usage/prices -> effective price per model, source (builtin|remote|override), fetched-at
```

3. Panel, web/management-center/src/features/usage/useUsageReport.ts:
```ts
const report = await usageHistoryApi.get({
  from: '2026-01-01', to: '2026-10-22', tz: Intl.DateTimeFormat().resolvedOptions().timeZone,
  bucket: 'day', groupBy: 'model', filter: { provider: 'claude' },
});
// report.enabled / report.reason
// report.buckets[i].start, .groups[j].key, .tokens {uncached, cacheRead, cacheWrite5m, cacheWrite1h, output, reasoning, unclassified},
//   .requests, .failures, .latencyMs (avg), .costUsd (null when unpriced)
// report.totals (same shape), report.unpricedModels: string[], report.keys: Record<hash, masked>
// CSV export is built client-side from the report and saved with downloadBlob.
```

## What the sketch must answer
- Package/module map and who owns which state (in-memory pending aggregates vs DB; one writer).
- SQLite schema + schema versioning (no migration framework exists in the repo), WAL, the write path (batched upsert on tick), retention cleanup, and how day/month buckets in a client time zone are built from UTC hours (DST-correct).
- Config section shape (name, v8 path, fields, defaults, clamps) and hot reload (pull model) incl. enable/disable and auth-dir change.
- Price catalog: embedded snapshot + daily remote refresh + overrides; where the fetched copy is cached; lookup rules for Record.Model (bare, provider-prefixed, overrides); which LiteLLM fields map to which token buckets; how 5m/1h cache-write split flows from the Claude parser through TokenBreakdown (or Detail) into the rollup.
- The shutdown-order fix.
- The quotaforecast duplicate-aggregation decision.
- Panel: page structure (container + prop-driven View), model files, chart(s), CSV.
