-- usage history schema, migration 1 (PRAGMA user_version 0 -> 1).
-- Embedded into store.go as migrations[0]; shown here for review.
--
-- Connection pragmas (every connection, via the modernc DSN _pragma params):
--   journal_mode = WAL          persistent; readers never block the writer
--   synchronous  = NORMAL       WAL-safe; a power cut loses at most the last commit
--   busy_timeout = 5000         local file-lock wait, not a network timeout
--   foreign_keys = OFF
-- Write handle DSN adds _txlock=immediate so the write tx takes the lock up front
-- (no SQLITE_BUSY on lock upgrade). Read handle: mode=ro, MaxOpenConns 4.
--
-- Set ONCE before the first table exists (only effective on an empty file):
PRAGMA auto_vacuum = INCREMENTAL;

-- Schema bookkeeping beyond user_version.
--   write_compat: the lowest binary schema version that may still WRITE this file.
--   Migrations that only add tables or NOT NULL DEFAULT columns leave it alone, so a
--   rolled-back binary (scripts/deploy.sh auto-rollback) keeps recording into a file
--   a newer binary already migrated. A migration that changes meaning raises it.
CREATE TABLE meta (
  k TEXT PRIMARY KEY,
  v TEXT NOT NULL
) WITHOUT ROWID;
INSERT INTO meta (k, v) VALUES ('write_compat', '1'), ('created_at', CAST(strftime('%s','now') AS TEXT));

-- One row per (UTC hour, provider, credential, model, client key, agent, tier).
-- WITHOUT ROWID: the table IS the primary-key b-tree, so the 6 strings are stored
-- once (no separate PK index duplicating them). hour leads the key, so every report
-- is a contiguous range scan. No secondary indexes: filters are applied to the
-- range-scanned rows; at 100x the measured volume a full year is ~1.8M rows.
CREATE TABLE rollup_hour (
  hour            INTEGER NOT NULL,              -- unix seconds, UTC hour start
  provider        TEXT    NOT NULL,
  auth_id         TEXT    NOT NULL,              -- '-' when none
  model           TEXT    NOT NULL,
  key_hash        TEXT    NOT NULL,              -- 12 hex or '-'
  agent           TEXT    NOT NULL,              -- '-' when none
  long_ctx        INTEGER NOT NULL DEFAULT 0 CHECK (long_ctx IN (0, 1)),
  requests        INTEGER NOT NULL DEFAULT 0,
  failures        INTEGER NOT NULL DEFAULT 0,
  uncached        INTEGER NOT NULL DEFAULT 0,
  cache_read      INTEGER NOT NULL DEFAULT 0,
  cache_write_5m  INTEGER NOT NULL DEFAULT 0,
  cache_write_1h  INTEGER NOT NULL DEFAULT 0,
  output          INTEGER NOT NULL DEFAULT 0,    -- non-reasoning output
  reasoning       INTEGER NOT NULL DEFAULT 0,
  unclassified    INTEGER NOT NULL DEFAULT 0,
  latency_ms_sum  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (hour, provider, auth_id, model, key_hash, agent, long_ctx)
) WITHOUT ROWID;

-- The last good remote price catalog, already trimmed (prices.Catalog encoding).
-- Lives in the same file so there is one file, one owner, one lifecycle, and the
-- git/object stores (which only mirror *.json) never see it.
CREATE TABLE price_cache (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  fetched_at  INTEGER NOT NULL,                  -- unix seconds of the 200/304 response
  etag        TEXT    NOT NULL DEFAULT '',       -- for If-None-Match on the next daily fetch
  body        BLOB    NOT NULL
);

PRAGMA user_version = 1;

-- ---------------------------------------------------------------------------
-- Write path (one tx per flush, prepared once per tx):
-- INSERT INTO rollup_hour (hour, provider, auth_id, model, key_hash, agent, long_ctx,
--   requests, failures, uncached, cache_read, cache_write_5m, cache_write_1h,
--   output, reasoning, unclassified, latency_ms_sum)
-- VALUES (?,?,?,?,?,?,?, ?,?,?,?,?,?,?,?,?,?)
-- ON CONFLICT (hour, provider, auth_id, model, key_hash, agent, long_ctx) DO UPDATE SET
--   requests = requests + excluded.requests, failures = failures + excluded.failures,
--   uncached = uncached + excluded.uncached, cache_read = cache_read + excluded.cache_read,
--   cache_write_5m = cache_write_5m + excluded.cache_write_5m,
--   cache_write_1h = cache_write_1h + excluded.cache_write_1h,
--   output = output + excluded.output, reasoning = reasoning + excluded.reasoning,
--   unclassified = unclassified + excluded.unclassified,
--   latency_ms_sum = latency_ms_sum + excluded.latency_ms_sum;
--
-- Report read (<g> is a column picked from a closed Go map, never user text;
-- '' AS g for group=none; filter clauses appended only when set):
-- SELECT hour, provider, model, long_ctx, <g> AS g,
--        SUM(requests), SUM(failures), SUM(uncached), SUM(cache_read), SUM(cache_write_5m),
--        SUM(cache_write_1h), SUM(output), SUM(reasoning), SUM(unclassified), SUM(latency_ms_sum)
-- FROM rollup_hour
-- WHERE hour >= ?lo AND hour < ?hi [AND provider = ?] [AND model = ?] [AND key_hash = ?]
--       [AND auth_id = ?] [AND agent = ?]
-- GROUP BY hour, provider, model, long_ctx, g;
-- (provider+model+long_ctx stay in the grouping because price depends on them even
--  when the report groups by key or agent. Rows out <= hours x price-keys x groups.)
--
-- Options read:  SELECT DISTINCT provider, model, key_hash, auth_id, agent FROM rollup_hour WHERE hour >= ? AND hour < ?;
-- Since read:    SELECT MIN(hour) FROM rollup_hour;
-- Hourly read:   SELECT hour, provider, auth_id, model, key_hash, agent, SUM(...) ... WHERE hour >= ? [AND provider IN (...)]
--                GROUP BY hour, provider, auth_id, model, key_hash, agent;
--
-- Retention (daily, and on the first tick after retention-days changes), one tx per
-- chunk so the write lock is never held for long:
--   lo := SELECT MIN(hour) FROM rollup_hour
--   for lo < cutoff: DELETE FROM rollup_hour WHERE hour >= lo AND hour < min(lo + 7*86400, cutoff); lo += 7*86400
--   PRAGMA incremental_vacuum;      -- return freed pages after a big retention cut
-- On close: PRAGMA optimize; PRAGMA wal_checkpoint(TRUNCATE).
