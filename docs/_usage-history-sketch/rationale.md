# Usage history: rationale (claude sketch)

Files (all under this folder):
- `go/internal/usagehistory/` doc.go (module map), types.go, query.go, plan.go, rollup.go, identity.go, store.go, schema.sql, service.go, report.go
- `go/internal/usagehistory/prices/` catalog.go, lookup.go, fetch.go
- `go/internal/config/usage_history_config.go`, `go/config.example.usage-history.yaml` (also AGENTS.md, cmd/trim-prices, go.mod notes)
- deltas: `go/sdk/cliproxy/usage/{accounting_delta,manager_delta}.go`, `go/internal/runtime/executor/helps/usage_helpers_delta.go`, `go/sdk/cliproxy/service_lifecycle_delta.go`, `go/internal/quotaforecast/usage_source.go`, `go/internal/api/handlers/management/usage_history.go`
- panel: `panel/src/services/api/usageHistory.ts`, `panel/src/features/usage/{usageModel,usageChartModel,usageCsv,useUsageReport}.ts`, `UsagePage.tsx`, `UsageView.tsx` (component tree in its header)
- `tests-plan.txt`

## 1. Shape

**Core types.** `Key` (UTC hour, provider, auth, model, key hash, agent, long-context tier) and `Counters` (requests, failures, seven disjoint `Tokens`, latency sum). Counters are additive, which makes the batched upsert, the restore after a failed write, and late records all the same operation: `+`. `Tokens` has seven disjoint buckets that sum to the total, so stacked charts and CSV columns never double count. `Report`/`Cell` come straight from the panel usage. The panel never does time-zone math: buckets come back as local RFC3339 strings.

**The run loop is the single writer.** There is one goroutine per Service. It does every SQLite write: flush, retention delete, migration, price-cache save, open and close. `HandleUsage` only reads an atomic flag and adds to a mutex-guarded `pending` map (no I/O). HTTP readers and quotaforecast do not run the refresh logic themselves, as requestlog does. They send a `sync` request on a channel. The loop then refreshes config, applies any transition, flushes pending, and replies. After that the reader queries the read pool under an RLock, which only blocks a close. Results:
- A report is current to the second, with no pending+DB merge and no chance of double counting.
- Enable, disable, and auth-dir changes are applied by the same goroutine, in the same order, whether a tick or a request triggered them. There is no "idempotent open under lock" logic to get wrong.
- Network work never runs on the loop. The daily price fetch runs in its own goroutine and hands its result back on `priceCh`.

**Lifecycle as types.** `phase` {Idle, Off, Open, Broken}, `want` (from config), `action`, and a pure `decide(status, want, now)` with the transition table in service.go. `Broken` is a real state: the DB cannot be opened (permissions, disk, a file from a newer version). The service keeps recording into bounded `pending` and retries once a minute, so a transient failure loses nothing.

**SQLite.** There is one `WITHOUT ROWID` table keyed `(hour, provider, auth_id, model, key_hash, agent, long_ctx)`, so every report is a contiguous primary-key range scan, with no secondary index and no string duplication. WAL, `synchronous=NORMAL`, a write handle with one connection and `_txlock=immediate`, and a read-only pool of 4. Schema versioning uses `PRAGMA user_version` plus a `write_compat` meta row. Migrations that only add things leave `write_compat` alone, so a binary that `scripts/deploy.sh` rolled back keeps writing to a file a newer binary already migrated. If `write_compat` is higher than the binary's version, the service goes to Broken instead of corrupting the file. Retention deletes run in 7-day chunks with `incremental_vacuum` after them (auto_vacuum=INCREMENTAL is set when the file is created). The price cache lives in the same file (`price_cache` table): one file, one owner, and the git/object stores, which only mirror `*.json`, never see it.

**Time zones.** `plan` computes local bucket boundaries in Go with `time.Date` in the client's zone. Days are 23h or 25h across DST because the next midnight is computed in civil time. Hour buckets step in absolute hours, so a fall-back day has 25 bars. Each UTC rollup hour goes to the bucket that contains its start. SQL only receives `[lo, hi)` on `hour` and groups by `(hour, provider, model, long_ctx, <group col>)`. Go assigns hours to buckets by binary search and prices each cell. `time/tzdata` is imported because Windows has no zoneinfo.

**Price catalog.**
- **Sources.** `prices` is a leaf package. `Trim` turns the full LiteLLM file into a compact `Catalog`. The same function produces the committed `litellm_trimmed.json` (go:embed, refreshed by `scripts/update-model-prices.sh` through `cmd/trim-prices`) and the daily remote copy, so there is one trimming path.
- **Daily fetch.** It runs from `RefreshInterval` after the last 200/304, sends `If-None-Match` with the cached ETag, has a 30s timeout (a new AGENTS.md exception), caps the body at 32 MB, and rejects results with fewer than 200 entries. After a failure it waits 1h before trying again. The cache is read back on open, so a restart does not refetch within the same day.
- **Overrides.** Configured in USD per million tokens. They can be partial ("only fix cache-write-1h") and can use `same-as` to map renamed ids (antigravity `-thinking`, `devin/...`) to a LiteLLM key.
- **Lookup order.** override(provider, model) → override(model) → catalog[model] → catalog[prefix/model] for the provider's LiteLLM prefixes. There is no fuzzy matching: a model with no match is listed as unpriced, which prompts an override, instead of getting a wrong price silently.
- **Field mapping and fallbacks.** Fallbacks are resolved when the catalog is parsed, so cost code never has to handle a missing field:
  - uncached → `input_cost_per_token`
  - cacheRead → `cache_read_input_token_cost`, else input
  - cacheWrite5m → `cache_creation_input_token_cost`, else input
  - cacheWrite1h → `cache_creation_input_token_cost_above_1hr`, else the 5m rate
  - output → `output_cost_per_token`
  - reasoning → `output_cost_per_reasoning_token`, else output
  - unclassified → never priced; counted in `unpriced_tokens`
  - `*_above_200k_tokens` fields → a `Long` rate set, used for rows with `long_ctx = 1`

**Long-context tier as a key column.** This is a decision the brief did not ask for. Claude 1M-context and Gemini Pro bill a premium on every token once the prompt passes 200k. An hourly aggregate cannot recover that later, so `long_ctx` is in the primary key from day one. It costs at most double the keys, and it keeps "a price change re-prices all history" accurate for long prompts.

**5m/1h cache-write split.**
- `TokenInputBreakdown` gets `CacheWrite1hTokens`, a subset of `CacheWriteTokens`. It is not a new sibling bucket.
- `Valid()` enforces `0 <= 1h <= CacheWrite`.
- `Detail` gets `CacheCreation1hTokens`, because stream merge and `EnsureTokenBreakdownForProvider` rebuild the breakdown from flat fields.
- `TokenBreakdown.WithCacheWrite1h(n)` is chained only by `parseClaudeUsageNode` and `MergeStreamUsageDetail`. The positional constructors are not changed.
- Every existing consumer keeps reading "all writes" unchanged. Schema version 2 is kept: the field is additive and `omitempty`, so old JSON reads as "all writes were default-TTL", which is what they were before the split.
- The rollup stores `5m = CacheWrite - 1h` and `1h`.
- No change to `internal/translator`.

**Shutdown-order fix (covers the whole class).**
- `Manager.Stop` now waits for the worker to exit. The worker already drains after `closed`; until now nobody waited for it.
- `Shutdown` now stops the HTTP server first (in-flight requests publish), then calls `usage.StopDefault()` (drain), then stops quotaforecast, requestlog and usagehistory (final flushes), then tears down the plugin host, so plugin usage sinks also get the tail.
- quotaforecast and requestlog used to stop before the server. They lost the same tail and now get it too.

**Quotaforecast: one path.** usagehistory becomes the only hourly usage aggregation:
- quotaforecast stops being a usage plugin. It drops `history.usage`, `HandleUsage`, `uncachedTokens` and `fileV1.Usage` (old files still decode).
- On provider-scoped forecasts it reads `usagehistory.Hourly(ctx, {Since, Providers})` and projects each row to its existing `UsageBucket{Tokens = total - cacheRead}`. `attribute()` is unchanged.
- `HashClientKey`/`AgentFamily` move to usagehistory to avoid an import cycle. requestlog's import changes with them.
- With usage history disabled, attribution returns `Method: "none", Reason: "usage_history_disabled"` and the panel explains it. Forecasts, alerts and hourly burn are unaffected.
- Cost of the change: about 12 hours of "proportional" attribution after deploy while history builds up. The old uncached-only buckets are not imported.

**Config.** `usage-history` at v8 path `observability.usage.usage-history`:
- `enabled`: default false
- `retention-days`: default 365, clamped to 1..3650
- `prices.disable-remote`
- `prices.remote-url`
- `prices.models[]`: model, provider, same-as, and six optional per-MTok rates

Hot reload uses the pull model: config is read on every tick and on every sync, so a change applies on the next request or within a minute. The full list of what each change does is in usage_history_config.go.

**Panel.**
- `UsagePage` is the container: controls state, `useUsageReport` (60s poll while visible and covering today, same stale guards as useQuotaForecast), lazy `useUsagePrices`, client labels through the existing `buildClientLabels`, and CSV through `downloadBlob`.
- `UsageView` is prop-driven: Disabled | Controls, Summary, Chart, GroupTable, Footnote, PriceTable.
- Pure models:
  - `usageModel.ts`: presets to query, bucket labels by string slicing, summary with "at least $X" when cost is partial
  - `usageChartModel.ts`: stacked bars by group (top 8 + other) or by token type, with equal-width bars so DST never distorts the chart
  - `usageCsv.ts`: RFC 4180, formula-injection guard, BOM, no raw keys
- Eight new `--usage-series-N` theme tokens, because no categorical palette exists. Token types get fixed colors.

## 2. Surface

Exposed:
- `Default`, `Start`, `Stop`, `HandleUsage`, `Report`, `Prices`, `Hourly`
- `ParseQuery`, the only boundary for raw strings
- `HashClientKey`, `AgentFamily`
- JSON types: `Report`, `Cell`, `Tokens`, `CostByType`, `PriceReport`
- `HourlyQuery`/`HourlyResult` for internal readers
- Two GET routes: `/observability/usage/history` and `/observability/usage/prices`

Report never returns an error: disabled or broken gives `{enabled:false, reason}` with HTTP 200, like the sibling services.

Hidden:
- SQL, the schema, versioning and compat, the two DB handles
- the pending map and its bound
- the run loop, sync channel and transitions
- the bucket plan
- price trimming, lookup rules, fallbacks, the fetch schedule and ETag
- retention chunking

Callers cannot write rollups, open the DB, or see a raw key.

## 3. Limits

- Hourly granularity: in :30/:45 offset zones, up to 30/45 minutes of usage shift across a local day boundary (assigned by hour start). Hour-bucket reports in those zones label the UTC hour by its local bucket.
- Cost is an estimate at public API prices and ignores:
  - OpenAI priority/flex tiers (`ResponseServiceTier`)
  - batch discounts
  - provider-specific regional pricing
  - Anthropic's long-context rule beyond the 200k threshold flag
- Unclassified tokens (Devin, unknown providers) are never priced. They show in `unpriced_tokens`.
- No per-request rows, so no drill-down to a request. That stays in requestlog (7-30 days).
- Group cardinality is not folded on the server. Fine for tens of keys or auths. Hundreds of groups by day over a year give a multi-MB response (the chart folds to top 8 + other; the table and CSV do not).
- Filters are single-valued exact matches. You cannot compare two models side by side except through group-by.
- Old `.quota-forecast.gz` usage buckets are discarded, not migrated.
- An auth-dir change starts a fresh file at the new location. The old file is not moved or merged.
- Records with `Generate=false` are not counted.
- If a usage plugin blocks inside HandleUsage, shutdown waits for it. This is bounded only by the process's 30s shutdown context.

## 4. Rejected

**Have SQLite produce the whole report**: a temp table of bucket boundaries joined to rollups for the time zone, and a temp prices table joined on (provider, model) for cost, so one SQL statement returns finished cells. It is fewer Go lines and fewer rows cross into Go. It lost because:
- The price lookup is a four-step ordered match with overrides and per-field fallbacks. In SQL that becomes a pile of COALESCE/LEFT JOINs that is hard to test, versus a pure Go function with table tests.
- "Unpriced vs partially priced" and the unpriced-model list are natural in Go and awkward in SQL.
- The row count after `GROUP BY hour, price key, group` is already bounded by hours × price keys × groups (about 35k for a year at today's volume, a few hundred thousand at 100x), so moving the work into SQL does not help.
- It would spread the time-zone logic over two languages, and DST correctness depends on Go's `time.Date`.

A smaller rejected variant: let reads merge the DB with the in-memory pending map. It has a double-count window during a flush that needs a second lock to close. The sync-then-read design removes that problem.

## 5. Questions (facts that could change the shape)

1. **Long-context traffic.** Do Zac's Claude requests exceed 200k prompt tokens, and does LiteLLM list `*_above_200k_tokens` for opus-5-5 and sonnet-5? If neither is true, `long_ctx` can be dropped from the key before the first release, which is the only time it is cheap to remove.
2. **Generate=false records.** Should they (count_tokens probes) count as requests? The sketch drops them. If Zac wants them visible, they need a flag column or a separate counter.
3. **Proxy for the fetch.** Does the registry model updater honour `proxy-url`? The price fetch should build its HTTP client the same way. The sketch assumes a `httpClient(cfg)` hook.
4. **Remote stores.** Does git-store or object-store mode ever sync files other than `*.json`, for example a whole-dir copy on bootstrap? The `.json` filters in gitstore.go:525 and objectstore.go:258 suggest not, but the bootstrap path was not checked. If it does, `.usage-history.db` and `-wal` need an explicit exclude.
5. **Attribution with usage history off.** Is losing quotaforecast attribution when usage history is off acceptable? The alternative is to have quota-forecast `enabled` imply usage-history recording, at the cost of hidden coupling.
6. **Retention max.** Is 3650 days right? It only bounds disk use (about 2 MB per year today).
7. **Auth labels.** Do the auth IDs that appear in reports read well as-is (file names), or should the report carry an `auths` label map like quotaforecast's `names`?
