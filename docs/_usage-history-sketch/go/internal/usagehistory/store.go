package usagehistory

import (
	"context"
	"database/sql"
	_ "embed"
	"time"

	_ "modernc.org/sqlite" // pure Go; release builds are CGO_ENABLED=0
)

// dbFileName starts with "." and is not *.json, so the auth watcher ignores it and
// the git/object stores (which walk *.json only) never mirror it. SQLite adds
// .usage-history.db-wal and -shm next to it; same properties.
const dbFileName = ".usage-history.db"

// schemaVersion is the newest schema this binary knows (len(migrations)).
const schemaVersion = 1

//go:embed schema.sql
var schemaV1 string

// migration upgrades user_version from index i to i+1 inside one transaction.
// writeCompat is the write_compat value after it runs: unchanged for additive
// migrations (new table, new NOT NULL DEFAULT column), raised for anything that
// changes the meaning of existing columns.
type migration struct {
	sql         string
	writeCompat int
}

// migrations is append-only. No framework: a slice and PRAGMA user_version.
var migrations = []migration{
	{sql: schemaV1, writeCompat: 1},
}

// store owns the two SQLite handles of one file.
//
//	write: MaxOpenConns 1, _txlock=immediate. Used ONLY from the run loop.
//	read:  MaxOpenConns 4, mode=ro. Used by Report/Hourly/Prices under Service.rw.RLock.
type store struct {
	path  string
	write *sql.DB
	read  *sql.DB
}

// errTooNew means the file was written by a newer binary whose write_compat is
// above this binary's schemaVersion. The service goes to phaseBroken with reason
// unavailable rather than touching the file.
type errTooNew struct{ fileVersion, writeCompat int }

func (e errTooNew) Error() string { return "usage history: database is from a newer version" }

// openStore opens (creating if absent) and migrates the file.
//
// pseudocode:
//
//	mkdir -p dir(path) 0700
//	write = sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_txlock=immediate")
//	write.SetMaxOpenConns(1)
//	v := PRAGMA user_version
//	switch {
//	case v == 0:          exec "PRAGMA auto_vacuum = INCREMENTAL" on write (before any table exists), then migrate 0 -> schemaVersion
//	case v < schemaVersion: migrate v -> schemaVersion, each step: BEGIN; exec m.sql; UPDATE meta write_compat; PRAGMA user_version = i+1; COMMIT
//	case v > schemaVersion: read meta.write_compat; if > schemaVersion -> close, return errTooNew; else log "newer schema, compatible" and continue
//	}
//	read = sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)"); read.SetMaxOpenConns(4)
//	chmod 0600 path (holds key hashes and credential IDs)
//
// A corrupt file (SQLITE_NOTADB / "malformed"): rename to path+".corrupt-<unix>" and
// create a fresh file, logging a warning. History is lost, recording continues.
func openStore(ctx context.Context, path string) (*store, error) { return nil, errNotImplemented }

// upsert writes one flush batch in a single transaction (see schema.sql for the
// statement). Additive, so a batch restored after a failed commit and re-sent
// with newer adds still sums correctly. A commit error means nothing was written
// (SQLite rolls back), so restore-and-retry cannot double count.
func (st *store) upsert(ctx context.Context, batch map[Key]Counters) error { return errNotImplemented }

// cleanupBefore deletes rows with hour < cutoff in 7-day chunks, one tx each,
// then runs PRAGMA incremental_vacuum. Returns rows deleted.
func (st *store) cleanupBefore(ctx context.Context, cutoff int64) (int64, error) {
	return 0, errNotImplemented
}

// scannedCell is one row of the report read: tokens of one (hour, price key, group).
type scannedCell struct {
	hour     int64
	provider string
	model    string
	longCtx  bool
	group    string
	counters Counters
}

// groupColumn maps a Dimension to its SQL column. Closed set: the only place a
// query string is assembled from a value that reached ParseQuery.
var groupColumn = map[Dimension]string{
	DimNone: "''", DimModel: "model", DimProvider: "provider",
	DimKey: "key_hash", DimAuth: "auth_id", DimAgent: "agent",
}

// scanCells runs the report read and streams rows to fn (no intermediate slice).
func (st *store) scanCells(ctx context.Context, lo, hi int64, f Filter, g Dimension, fn func(scannedCell)) error {
	return errNotImplemented
}

// distinct returns the filter options present in [lo, hi).
func (st *store) distinct(ctx context.Context, lo, hi int64) (Options, error) {
	return Options{}, errNotImplemented
}

// priceKeys returns the distinct (provider, model) pairs with hour >= since, for the prices endpoint.
func (st *store) priceKeys(ctx context.Context, since int64) ([][2]string, error) {
	return nil, errNotImplemented
}

// oldestHour returns MIN(hour), ok=false when empty.
func (st *store) oldestHour(ctx context.Context) (int64, bool, error) { return 0, false, errNotImplemented }

// scanHourly runs the Hourly read for quotaforecast.
func (st *store) scanHourly(ctx context.Context, since int64, providers []string) ([]HourlyRow, error) {
	return nil, errNotImplemented
}

// loadPriceCache returns the cached remote catalog body, its fetch time and etag; ok=false when none.
func (st *store) loadPriceCache(ctx context.Context) (body []byte, fetchedAt time.Time, etag string, ok bool, err error) {
	return nil, time.Time{}, "", false, errNotImplemented
}

// savePriceCache replaces the cached catalog (INSERT OR REPLACE id=1). A 304 only
// updates fetched_at.
func (st *store) savePriceCache(ctx context.Context, body []byte, fetchedAt time.Time, etag string) error {
	return errNotImplemented
}

// close runs PRAGMA optimize and wal_checkpoint(TRUNCATE) on write, then closes
// both handles. Errors are joined and returned; the caller logs them.
func (st *store) close() error { return errNotImplemented }
