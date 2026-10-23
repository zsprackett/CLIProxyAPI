-- Usage history schema, migration 1 (PRAGMA user_version 0 -> 1).
-- The write handle DSN sets auto_vacuum = INCREMENTAL before the first table
-- exists. openStore runs this in one transaction and sets user_version itself.

-- write_compat: the lowest binary schema version that may still WRITE this file.
-- Migrations that only add tables or NOT NULL DEFAULT columns leave it alone, so a
-- rolled-back binary (scripts/deploy.sh auto-rollback) keeps recording into a file
-- a newer binary already migrated. A migration that changes meaning raises it.
CREATE TABLE meta (
  k TEXT PRIMARY KEY,
  v TEXT NOT NULL
) WITHOUT ROWID;
INSERT INTO meta (k, v) VALUES ('write_compat', '1'), ('created_at', CAST(strftime('%s','now') AS TEXT));

-- One row per (UTC hour, provider, credential, model, client key, agent, tier).
-- WITHOUT ROWID: the table is the primary-key b-tree, so the strings are stored
-- once. hour leads the key, so every report is a contiguous range scan. No
-- secondary indexes: filters apply to the range-scanned rows.
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
-- It lives in the same file so there is one file with one owner, and the
-- git/object stores (which only mirror *.json) never see it.
CREATE TABLE price_cache (
  id          INTEGER PRIMARY KEY CHECK (id = 1),
  fetched_at  INTEGER NOT NULL,                  -- unix seconds of the 200/304 response
  etag        TEXT    NOT NULL DEFAULT '',       -- If-None-Match for the next fetch
  body        BLOB    NOT NULL
);
