# Approved decisions (Zac, 2026-10-22)

This sketch (the "claude" sketch) is the approved contract. These decisions add to it or override it.

## From Zac
- Built in, not a plugin. Scope: long history, full token breakdown, cost, a Usage page.
- Storage is embedded SQLite through `modernc.org/sqlite` (pure Go).
- Only hourly rollups are kept for `retention-days`, default 365, clamp 1..3650. No per-request rows in SQLite.
- Prices come from three layers: an embedded trimmed LiteLLM snapshot, a daily LiteLLM fetch, and overrides in config.yaml. The fetch timeout is a named exception in AGENTS.md.
- Claude cache writes are split into 5m and 1h.

## Taken from the other sketches
- Response limits (from grok). Each report keeps the top 30 groups by cost, then by tokens, over the range. The rest fold into one group with key `"*other*"` in `buckets[].groups`, `groups` and the CSV. The report gets `folded_groups int` (the number folded, 0 when none). The span limits per unit stay as in query.go. A report that would have more than 50,000 (bucket x group) cells after folding returns 400 and names the limit.
- A property test (from the event-log sketch). For any set of records, `Report` before a flush and after it must be byte-identical (the pending part is flushed by sync in both cases), and the result must equal a reference fold over the raw records.

## Defaults chosen and shown to Zac
- `long_ctx` = 1 when `TokenBreakdown.Input.TotalTokens > 200_000` (the prompt side). 19% of opus-5-5 traffic qualifies. grok-4.7 has above-200k rates in LiteLLM.
- Records with `Generate=false` (count_tokens probes) are not counted.
- `usage-history.enabled` defaults to false. The live config enables it at deploy.
- quotaforecast reads attribution buckets from `usagehistory.Hourly`. When usage history is off, attribution is `Method: "none", Reason: "usage_history_<reason>"`.
- The price fetch uses a plain `http.Client` with a 30s timeout, like the registry model updater (no proxy-url).
- Git-store and object-store modes commit and list only named `*.json` files, so `.usage-history.db` is not mirrored. This was checked in internal/store/gitstore.go and objectstore.go.

## Rejected
- The grok sketch: HTTP goroutines open and close the DB through refreshEnabled, and three ordered locks. The cache-split helper sits in the executor/helps package.
- The event-log sketch (claude-events): it stores per-request rows, which goes against the rollups-only decision, and has 5 to 10 times the disk writes.
