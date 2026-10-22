package usagehistory

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// dbFileName starts with "." and is not *.json, so the auth watcher ignores it and
// the git/object stores (which walk *.json only) never mirror it. SQLite adds
// .usage-history.db-wal and -shm next to it, with the same properties.
const dbFileName = ".usage-history.db"

// schemaVersion is the newest schema this binary knows (len(migrations)).
const schemaVersion = 1

// retentionChunk is the hour span one retention DELETE transaction covers, so
// the write lock is never held for long.
const retentionChunk = 7 * 86400

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

// migrations is append-only.
var migrations = []migration{
	{sql: schemaV1, writeCompat: 1},
}

// store owns the two SQLite handles of one file.
//
//	write: MaxOpenConns 1, _txlock=immediate. Used only from the run loop.
//	read:  MaxOpenConns 4, mode=ro. Used by Report/Hourly/Prices under Service.rw.RLock.
type store struct {
	path  string
	write *sql.DB
	read  *sql.DB
}

// errTooNew means the file was written by a newer binary whose write_compat is
// above this binary's schemaVersion. The service goes to phaseBroken rather than
// touching the file.
type errTooNew struct{ fileVersion, writeCompat int }

func (e errTooNew) Error() string {
	return fmt.Sprintf("usage history: database schema %d needs a binary that writes schema %d or newer, this one writes %d",
		e.fileVersion, e.writeCompat, schemaVersion)
}

// dsn builds a SQLite URI for path. The path is escaped so "?" or "#" in an auth
// dir cannot leak into the parameters.
func dsn(path string, params string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p // Windows drive letter: file:///C:/...
	}
	return "file://" + (&url.URL{Path: p}).EscapedPath() + "?" + params
}

// auto_vacuum comes first: it only takes effect on a file with no tables yet, and
// switching to WAL writes the header, after which it would be too late.
const (
	writeParams = "_pragma=auto_vacuum(INCREMENTAL)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_txlock=immediate"
	readParams  = "mode=ro&_pragma=busy_timeout(5000)"
)

// openStore opens (creating if absent) and migrates the file. A file that is not
// a SQLite database is renamed to path+".corrupt-<unix>" and replaced with a
// fresh one: history is lost, recording continues.
func openStore(ctx context.Context, path string) (*store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("usage history: create dir: %w", err)
	}
	if info, err := os.Stat(path); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("usage history: %s is not a regular file", path)
	}
	st, err := openStoreOnce(ctx, path)
	if err != nil && isCorrupt(err) {
		moved := fmt.Sprintf("%s.corrupt-%d", path, time.Now().Unix())
		log.WithError(err).Warnf("usage history: %s is not a usable database; moving it to %s and starting a new file", path, moved)
		if errRename := os.Rename(path, moved); errRename != nil {
			return nil, fmt.Errorf("usage history: move corrupt file: %w", errRename)
		}
		for _, suffix := range []string{"-wal", "-shm"} {
			if errRemove := os.Remove(path + suffix); errRemove != nil && !errors.Is(errRemove, os.ErrNotExist) {
				log.WithError(errRemove).Warnf("usage history: remove stale %s", path+suffix)
			}
		}
		st, err = openStoreOnce(ctx, path)
	}
	return st, err
}

func isCorrupt(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "not a database") || strings.Contains(msg, "malformed")
}

func openStoreOnce(ctx context.Context, path string) (st *store, err error) {
	write, err := sql.Open("sqlite", dsn(path, writeParams))
	if err != nil {
		return nil, fmt.Errorf("usage history: open: %w", err)
	}
	write.SetMaxOpenConns(1)
	defer func() {
		if err != nil {
			if errClose := write.Close(); errClose != nil {
				log.WithError(errClose).Debug("usage history: close after failed open")
			}
		}
	}()
	if err = migrate(ctx, write); err != nil {
		return nil, err
	}
	read, err := sql.Open("sqlite", dsn(path, readParams))
	if err != nil {
		return nil, fmt.Errorf("usage history: open read pool: %w", err)
	}
	read.SetMaxOpenConns(4)
	if err = os.Chmod(path, 0o600); err != nil {
		log.WithError(err).Warn("usage history: restrict file mode")
		err = nil
	}
	return &store{path: path, write: write, read: read}, nil
}

// migrate brings user_version up to schemaVersion, or refuses a file a newer
// binary made incompatible.
func migrate(ctx context.Context, db *sql.DB) error {
	var v int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return fmt.Errorf("usage history: read schema version: %w", err)
	}
	if v > schemaVersion {
		var compat string
		if err := db.QueryRowContext(ctx, "SELECT v FROM meta WHERE k = 'write_compat'").Scan(&compat); err != nil {
			return fmt.Errorf("usage history: read write_compat: %w", err)
		}
		n, errAtoi := strconv.Atoi(compat)
		if errAtoi != nil || n > schemaVersion {
			return errTooNew{fileVersion: v, writeCompat: n}
		}
		log.Infof("usage history: database schema %d is newer than %d but still writable", v, schemaVersion)
		return nil
	}
	for i := v; i < schemaVersion; i++ {
		if err := runMigration(ctx, db, i, migrations[i]); err != nil {
			return err
		}
	}
	return nil
}

func runMigration(ctx context.Context, db *sql.DB, from int, m migration) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("usage history: migrate %d: begin: %w", from, err)
	}
	defer func() {
		if err != nil {
			if errRollback := tx.Rollback(); errRollback != nil {
				log.WithError(errRollback).Debug("usage history: migration rollback")
			}
		}
	}()
	if _, err = tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("usage history: migrate %d: %w", from, err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR REPLACE INTO meta (k, v) VALUES ('write_compat', ?)", strconv.Itoa(m.writeCompat)); err != nil {
		return fmt.Errorf("usage history: migrate %d: write_compat: %w", from, err)
	}
	if _, err = tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", from+1)); err != nil {
		return fmt.Errorf("usage history: migrate %d: user_version: %w", from, err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("usage history: migrate %d: commit: %w", from, err)
	}
	return nil
}

const upsertSQL = `INSERT INTO rollup_hour (hour, provider, auth_id, model, key_hash, agent, long_ctx,
  requests, failures, uncached, cache_read, cache_write_5m, cache_write_1h,
  output, reasoning, unclassified, latency_ms_sum)
VALUES (?,?,?,?,?,?,?, ?,?,?,?,?,?,?,?,?,?)
ON CONFLICT (hour, provider, auth_id, model, key_hash, agent, long_ctx) DO UPDATE SET
  requests = requests + excluded.requests, failures = failures + excluded.failures,
  uncached = uncached + excluded.uncached, cache_read = cache_read + excluded.cache_read,
  cache_write_5m = cache_write_5m + excluded.cache_write_5m,
  cache_write_1h = cache_write_1h + excluded.cache_write_1h,
  output = output + excluded.output, reasoning = reasoning + excluded.reasoning,
  unclassified = unclassified + excluded.unclassified,
  latency_ms_sum = latency_ms_sum + excluded.latency_ms_sum`

// upsert writes one flush batch in a single transaction. Additive, so a batch
// restored after a failed commit and re-sent with newer adds still sums
// correctly. A commit error means nothing was written (SQLite rolls back), so
// restore-and-retry cannot double count.
func (st *store) upsert(ctx context.Context, batch map[Key]Counters) (err error) {
	if len(batch) == 0 {
		return nil
	}
	tx, err := st.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("usage history: upsert: begin: %w", err)
	}
	defer func() {
		if err != nil {
			if errRollback := tx.Rollback(); errRollback != nil && !errors.Is(errRollback, sql.ErrTxDone) {
				log.WithError(errRollback).Debug("usage history: upsert rollback")
			}
		}
	}()
	stmt, err := tx.PrepareContext(ctx, upsertSQL)
	if err != nil {
		return fmt.Errorf("usage history: upsert: prepare: %w", err)
	}
	defer func() {
		if errClose := stmt.Close(); errClose != nil {
			log.WithError(errClose).Debug("usage history: close upsert statement")
		}
	}()
	for k, c := range batch {
		t := c.Tokens
		if _, err = stmt.ExecContext(ctx, k.Hour, k.Provider, k.AuthID, k.Model, k.KeyHash, k.Agent, boolInt(k.LongCtx),
			c.Requests, c.Failures, t.Uncached, t.CacheRead, t.CacheWrite5m, t.CacheWrite1h,
			t.Output, t.Reasoning, t.Unclassified, c.LatencyMsSum); err != nil {
			return fmt.Errorf("usage history: upsert: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("usage history: upsert: commit: %w", err)
	}
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// cleanupBefore deletes rows with hour < cutoff in 7-day chunks, one transaction
// each, then runs PRAGMA incremental_vacuum. Returns rows deleted.
func (st *store) cleanupBefore(ctx context.Context, cutoff int64) (int64, error) {
	lo, ok, err := oldestHourOf(ctx, st.write)
	if err != nil || !ok {
		return 0, err
	}
	var deleted int64
	for ; lo < cutoff; lo += retentionChunk {
		res, errExec := st.write.ExecContext(ctx, "DELETE FROM rollup_hour WHERE hour >= ? AND hour < ?", lo, min(lo+retentionChunk, cutoff))
		if errExec != nil {
			return deleted, fmt.Errorf("usage history: retention delete: %w", errExec)
		}
		n, errRows := res.RowsAffected()
		if errRows == nil {
			deleted += n
		}
	}
	if deleted > 0 {
		if _, errVacuum := st.write.ExecContext(ctx, "PRAGMA incremental_vacuum"); errVacuum != nil {
			return deleted, fmt.Errorf("usage history: incremental vacuum: %w", errVacuum)
		}
	}
	return deleted, nil
}

// scannedCell is one row of the report read: counters of one (hour, price key, group).
type scannedCell struct {
	hour     int64
	provider string
	model    string
	longCtx  bool
	group    string
	counters Counters
}

// groupColumn maps a Dimension to its SQL expression. Closed set: the only place a
// query string is assembled from a value that reached ParseQuery.
var groupColumn = map[Dimension]string{
	DimNone: "'all'", DimModel: "model", DimProvider: "provider",
	DimKey: "key_hash", DimAuth: "auth_id", DimAgent: "agent",
}

const sumColumns = `SUM(requests), SUM(failures), SUM(uncached), SUM(cache_read), SUM(cache_write_5m),
  SUM(cache_write_1h), SUM(output), SUM(reasoning), SUM(unclassified), SUM(latency_ms_sum)`

func sumTargets(c *Counters) []any {
	t := &c.Tokens
	return []any{&c.Requests, &c.Failures, &t.Uncached, &t.CacheRead, &t.CacheWrite5m,
		&t.CacheWrite1h, &t.Output, &t.Reasoning, &t.Unclassified, &c.LatencyMsSum}
}

// scanCells runs the report read and streams rows to fn. provider, model and
// long_ctx stay in the grouping because the price depends on them.
func (st *store) scanCells(ctx context.Context, lo, hi int64, f Filter, g Dimension, fn func(scannedCell)) error {
	col, ok := groupColumn[g]
	if !ok {
		return fmt.Errorf("usage history: unknown group %q", g)
	}
	where := []string{"hour >= ?", "hour < ?"}
	args := []any{lo, hi}
	for _, c := range []struct{ col, value string }{
		{"provider", f.Provider}, {"model", f.Model}, {"key_hash", f.KeyHash}, {"auth_id", f.AuthID}, {"agent", f.Agent},
	} {
		if c.value != "" {
			where = append(where, c.col+" = ?")
			args = append(args, c.value)
		}
	}
	query := "SELECT hour, provider, model, long_ctx, " + col + " AS g, " + sumColumns +
		" FROM rollup_hour WHERE " + strings.Join(where, " AND ") +
		" GROUP BY hour, provider, model, long_ctx, g"
	rows, err := st.read.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("usage history: report read: %w", err)
	}
	defer closeRows(rows)
	for rows.Next() {
		var c scannedCell
		var long int
		targets := append([]any{&c.hour, &c.provider, &c.model, &long, &c.group}, sumTargets(&c.counters)...)
		if err = rows.Scan(targets...); err != nil {
			return fmt.Errorf("usage history: report scan: %w", err)
		}
		c.longCtx = long == 1
		fn(c)
	}
	if err = rows.Err(); err != nil {
		return fmt.Errorf("usage history: report read: %w", err)
	}
	return nil
}

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		log.WithError(err).Debug("usage history: close rows")
	}
}

// distinct returns the filter options present in [lo, hi), each sorted.
func (st *store) distinct(ctx context.Context, lo, hi int64) (Options, error) {
	rows, err := st.read.QueryContext(ctx,
		"SELECT DISTINCT provider, model, key_hash, auth_id, agent FROM rollup_hour WHERE hour >= ? AND hour < ?", lo, hi)
	if err != nil {
		return Options{}, fmt.Errorf("usage history: options read: %w", err)
	}
	defer closeRows(rows)
	sets := [5]map[string]struct{}{{}, {}, {}, {}, {}}
	for rows.Next() {
		var v [5]string
		if err = rows.Scan(&v[0], &v[1], &v[2], &v[3], &v[4]); err != nil {
			return Options{}, fmt.Errorf("usage history: options scan: %w", err)
		}
		for i := range v {
			sets[i][v[i]] = struct{}{}
		}
	}
	if err = rows.Err(); err != nil {
		return Options{}, fmt.Errorf("usage history: options read: %w", err)
	}
	return Options{
		Provider: sortedKeys(sets[0]), Model: sortedKeys(sets[1]), Key: sortedKeys(sets[2]),
		Auth: sortedKeys(sets[3]), Agent: sortedKeys(sets[4]),
	}, nil
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// priceKeys returns the distinct (provider, model) pairs with hour >= since.
func (st *store) priceKeys(ctx context.Context, since int64) ([][2]string, error) {
	rows, err := st.read.QueryContext(ctx,
		"SELECT DISTINCT provider, model FROM rollup_hour WHERE hour >= ? ORDER BY provider, model", since)
	if err != nil {
		return nil, fmt.Errorf("usage history: price keys read: %w", err)
	}
	defer closeRows(rows)
	var out [][2]string
	for rows.Next() {
		var k [2]string
		if err = rows.Scan(&k[0], &k[1]); err != nil {
			return nil, fmt.Errorf("usage history: price keys scan: %w", err)
		}
		out = append(out, k)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("usage history: price keys read: %w", err)
	}
	return out, nil
}

// oldestHour returns MIN(hour), ok=false when empty.
func (st *store) oldestHour(ctx context.Context) (int64, bool, error) {
	return oldestHourOf(ctx, st.read)
}

func oldestHourOf(ctx context.Context, db *sql.DB) (int64, bool, error) {
	var hour sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT MIN(hour) FROM rollup_hour").Scan(&hour); err != nil {
		return 0, false, fmt.Errorf("usage history: oldest hour: %w", err)
	}
	return hour.Int64, hour.Valid, nil
}

// scanHourly runs the Hourly read for quotaforecast: tiers merged.
func (st *store) scanHourly(ctx context.Context, since int64, providers []string) ([]HourlyRow, error) {
	query := "SELECT hour, provider, auth_id, model, key_hash, agent, " + sumColumns + " FROM rollup_hour WHERE hour >= ?"
	args := []any{since}
	if len(providers) > 0 {
		query += " AND provider IN (?" + strings.Repeat(",?", len(providers)-1) + ")"
		for _, p := range providers {
			args = append(args, p)
		}
	}
	query += " GROUP BY hour, provider, auth_id, model, key_hash, agent"
	rows, err := st.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("usage history: hourly read: %w", err)
	}
	defer closeRows(rows)
	var out []HourlyRow
	for rows.Next() {
		var r HourlyRow
		var c Counters
		targets := append([]any{&r.Hour, &r.Provider, &r.AuthID, &r.Model, &r.KeyHash, &r.Agent}, sumTargets(&c)...)
		if err = rows.Scan(targets...); err != nil {
			return nil, fmt.Errorf("usage history: hourly scan: %w", err)
		}
		r.Requests, r.Failures, r.Tokens = c.Requests, c.Failures, c.Tokens
		out = append(out, r)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("usage history: hourly read: %w", err)
	}
	return out, nil
}

// loadPriceCache returns the cached remote catalog body, its fetch time and etag; ok=false when none.
func (st *store) loadPriceCache(ctx context.Context) (body []byte, fetchedAt time.Time, etag string, ok bool, err error) {
	var at int64
	err = st.write.QueryRowContext(ctx, "SELECT body, fetched_at, etag FROM price_cache WHERE id = 1").Scan(&body, &at, &etag)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, "", false, nil
	}
	if err != nil {
		return nil, time.Time{}, "", false, fmt.Errorf("usage history: load price cache: %w", err)
	}
	return body, time.Unix(at, 0), etag, true, nil
}

// savePriceCache replaces the cached catalog. A nil body (HTTP 304) only
// updates fetched_at and etag.
func (st *store) savePriceCache(ctx context.Context, body []byte, fetchedAt time.Time, etag string) error {
	var err error
	if body == nil {
		_, err = st.write.ExecContext(ctx, "UPDATE price_cache SET fetched_at = ?, etag = ? WHERE id = 1", fetchedAt.Unix(), etag)
	} else {
		_, err = st.write.ExecContext(ctx, "INSERT OR REPLACE INTO price_cache (id, fetched_at, etag, body) VALUES (1, ?, ?, ?)",
			fetchedAt.Unix(), etag, body)
	}
	if err != nil {
		return fmt.Errorf("usage history: save price cache: %w", err)
	}
	return nil
}

// close runs PRAGMA optimize and wal_checkpoint(TRUNCATE) on the write handle,
// then closes both handles. Errors are joined; the caller logs them.
func (st *store) close() error {
	var errs []error
	if err := st.read.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close read pool: %w", err))
	}
	for _, pragma := range []string{"PRAGMA optimize", "PRAGMA wal_checkpoint(TRUNCATE)"} {
		if _, err := st.write.Exec(pragma); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", pragma, err))
		}
	}
	if err := st.write.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close write handle: %w", err))
	}
	return errors.Join(errs...)
}
