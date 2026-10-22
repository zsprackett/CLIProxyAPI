// Package usagehistory keeps hourly usage rollups in an embedded SQLite file
// (<auth-dir>/.usage-history.db) for up to a year or more, prices them at read
// time from a LiteLLM-derived catalog, and serves time-zone-aware reports.
//
// Public surface (everything a caller can touch):
//
//	Default() *Service
//	(*Service).Start(ctx, *coreauth.Manager)
//	(*Service).Stop(ctx)
//	(*Service).HandleUsage(ctx, usage.Record)              // usage.Plugin; memory only, never I/O
//	(*Service).Report(ctx, Query) Report                   // GET /observability/usage/history
//	(*Service).Prices(ctx) PriceReport                     // GET /observability/usage/prices
//	(*Service).Hourly(ctx, HourlyQuery) HourlyResult       // raw hourly rows for quotaforecast attribution
//	ParseQuery(url.Values) (Query, error)                  // HTTP boundary
//	HashClientKey(string) string, AgentFamily(string) string   // moved here from quotaforecast
//	Query, Report, Cell, Tokens, CostByType, HourlyQuery, HourlyResult, HourlyRow, PriceReport
//
// Hidden: the SQL schema and its versioning, the write connection, the pending
// map, the bucket plan, price lookup rules, the remote fetch schedule, clamping.
//
// Module map (state owner -> what it exposes inside the package):
//
//	Service  service.go  owns lifecycle status {phase, path, reason, *store},       -> public surface above
//	                     the price table pointer, and the run loop (THE ONE WRITER)
//	pending  rollup.go   owns map[Key]Counters since the last flush;                 -> add, swap, restore
//	                     the only state HandleUsage touches (own mutex)
//	store    store.go    owns the SQLite handles (1 write conn + read pool),        -> openStore, upsert, cleanupBefore,
//	                     schema version, write-compat level                          scanCells, scanHourly, distinct, priceCache
//	plan     plan.go     pure: civil range + tz + unit -> UTC bucket boundaries     -> newPlan, index, label
//	report   report.go   pure: scanned cells + price table + plan -> Report         -> buildReport, costOf
//	prices/  leaf pkg    pure data + one fetch function; no package state           -> Trim, Decode, Fetch, NewTable, Table.Lookup
//
// One writer: every SQLite write (rollup upsert, retention delete, price cache
// save, schema migration, close) happens on the run-loop goroutine. HTTP readers
// and quotaforecast ask the loop to sync (refresh config + flush pending) through
// a channel, then read through the read pool under an RLock that only blocks a
// close. Because the flush happens on the loop before the read, a report is
// always current to the moment it was requested and nothing is ever read from
// two places at once (no pending+DB merge, no double count).
//
// Data flow:
//
//	usage.Record --HandleUsage--> rollupOf --> pending[Key] += Counters        (worker goroutine, no I/O)
//	tick | sync --> loop: refresh(cfg) -> transition -> pending.swap -> store.upsert (one tx)
//	Report(q): sync -> plan(q) -> store.scanCells(range, filter, group) -> buildReport(cells, plan, prices.Table) -> JSON
package usagehistory
