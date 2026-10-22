package usagehistory

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestStore(t *testing.T, path string) *store {
	t.Helper()
	st, err := openStore(context.Background(), path)
	if err != nil {
		t.Fatalf("openStore: %v", err)
	}
	t.Cleanup(func() {
		if st.write != nil {
			_ = st.close()
		}
	})
	return st
}

func closeStore(t *testing.T, st *store) {
	t.Helper()
	if err := st.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	st.write = nil
}

// rawDB opens the file outside the store, to inspect or tamper with it.
func rawDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dsn(path, "_pragma=busy_timeout(5000)"))
	if err != nil {
		t.Fatalf("open raw: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func pragmaInt(t *testing.T, db *sql.DB, name string) int {
	t.Helper()
	var v int
	if err := db.QueryRow("PRAGMA " + name).Scan(&v); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return v
}

func testKey(hour int64, model string) Key {
	return Key{Hour: hour, Provider: "claude", AuthID: "a.json", Model: model, KeyHash: "abc", Agent: "claude-cli"}
}

func TestStoreMigratesAnEmptyFile(t *testing.T) {
	// The auth dir may hold characters that mean something in a SQLite URI.
	path := filepath.Join(t.TempDir(), "sub dir?x=1#y", dbFileName)
	st := openTestStore(t, path)
	closeStore(t, st)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file mode = %o, want 600", perm)
	}
	db := rawDB(t, path)
	if v := pragmaInt(t, db, "user_version"); v != schemaVersion {
		t.Fatalf("user_version = %d, want %d", v, schemaVersion)
	}
	if v := pragmaInt(t, db, "auto_vacuum"); v != 2 {
		t.Fatalf("auto_vacuum = %d, want 2 (incremental)", v)
	}
	var compat string
	if err := db.QueryRow("SELECT v FROM meta WHERE k = 'write_compat'").Scan(&compat); err != nil || compat != "1" {
		t.Fatalf("write_compat = %q, %v", compat, err)
	}
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, %v", mode, err)
	}
}

func TestStoreReopenKeepsRows(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), dbFileName)
	st := openTestStore(t, path)
	if err := st.upsert(ctx, map[Key]Counters{testKey(3600, "m"): {Requests: 2, Tokens: Tokens{Output: 9}}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	closeStore(t, st)

	st = openTestStore(t, path)
	rows, err := st.scanHourly(ctx, 0, nil)
	if err != nil {
		t.Fatalf("scanHourly: %v", err)
	}
	if len(rows) != 1 || rows[0].Requests != 2 || rows[0].Tokens.Output != 9 {
		t.Fatalf("rows after reopen = %+v", rows)
	}
}

func TestStoreAdditiveUpsert(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, filepath.Join(t.TempDir(), dbFileName))
	k := testKey(7200, "m")
	long := k
	long.LongCtx = true
	batch := map[Key]Counters{
		k:    {Requests: 1, Failures: 1, LatencyMsSum: 10, Tokens: Tokens{Uncached: 1, CacheRead: 2, CacheWrite5m: 3, CacheWrite1h: 4, Output: 5, Reasoning: 6, Unclassified: 7}},
		long: {Requests: 1, Tokens: Tokens{Uncached: 100}},
	}
	for i := 0; i < 2; i++ {
		if err := st.upsert(ctx, batch); err != nil {
			t.Fatalf("upsert %d: %v", i, err)
		}
	}
	rows, err := st.scanHourly(ctx, 0, nil)
	if err != nil {
		t.Fatalf("scanHourly: %v", err)
	}
	// Hourly merges the long-context tier.
	want := Tokens{Uncached: 202, CacheRead: 4, CacheWrite5m: 6, CacheWrite1h: 8, Output: 10, Reasoning: 12, Unclassified: 14}
	if len(rows) != 1 || rows[0].Tokens != want || rows[0].Requests != 4 || rows[0].Failures != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	var cells []scannedCell
	if err := st.scanCells(ctx, 0, 1<<40, Filter{}, DimModel, func(c scannedCell) { cells = append(cells, c) }); err != nil {
		t.Fatalf("scanCells: %v", err)
	}
	if len(cells) != 2 {
		t.Fatalf("cells = %+v, want the two tiers apart", cells)
	}
	for _, c := range cells {
		if c.group != "m" || c.counters.Requests != 2 {
			t.Fatalf("cell = %+v", c)
		}
		if c.longCtx && c.counters.LatencyMsSum != 0 || !c.longCtx && c.counters.LatencyMsSum != 20 {
			t.Fatalf("cell = %+v", c)
		}
	}
}

func TestStoreScanFiltersAndGroups(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, filepath.Join(t.TempDir(), dbFileName))
	a := testKey(3600, "m1")
	b := testKey(3600, "m2")
	b.KeyHash = "def"
	c := testKey(7200, "m1")
	c.Provider = "codex"
	if err := st.upsert(ctx, map[Key]Counters{a: {Requests: 1}, b: {Requests: 2}, c: {Requests: 4}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	sum := func(lo, hi int64, f Filter, g Dimension) map[string]int64 {
		out := map[string]int64{}
		if err := st.scanCells(ctx, lo, hi, f, g, func(c scannedCell) { out[c.group] += c.counters.Requests }); err != nil {
			t.Fatalf("scanCells: %v", err)
		}
		return out
	}
	if got := sum(0, 1<<40, Filter{}, DimKey); got["abc"] != 5 || got["def"] != 2 {
		t.Fatalf("by key = %v", got)
	}
	if got := sum(0, 1<<40, Filter{Provider: "claude"}, DimNone); len(got) != 1 || got["all"] != 3 {
		t.Fatalf("claude only = %v", got)
	}
	if got := sum(3600, 7200, Filter{}, DimProvider); got["claude"] != 3 || got["codex"] != 0 {
		t.Fatalf("range = %v", got)
	}
	if got := sum(0, 1<<40, Filter{Model: "m1", KeyHash: "abc", AuthID: "a.json", Agent: "claude-cli"}, DimAgent); got["claude-cli"] != 5 {
		t.Fatalf("all filters = %v", got)
	}

	opts, err := st.distinct(ctx, 0, 1<<40)
	if err != nil {
		t.Fatalf("distinct: %v", err)
	}
	if strings.Join(opts.Provider, ",") != "claude,codex" || strings.Join(opts.Model, ",") != "m1,m2" || strings.Join(opts.Key, ",") != "abc,def" {
		t.Fatalf("options = %+v", opts)
	}
	keys, err := st.priceKeys(ctx, 0)
	if err != nil || len(keys) != 3 {
		t.Fatalf("priceKeys = %v, %v", keys, err)
	}
	oldest, ok, err := st.oldestHour(ctx)
	if err != nil || !ok || oldest != 3600 {
		t.Fatalf("oldestHour = %d %v %v", oldest, ok, err)
	}
	rows, err := st.scanHourly(ctx, 0, []string{"codex"})
	if err != nil || len(rows) != 1 || rows[0].Provider != "codex" {
		t.Fatalf("hourly codex = %+v, %v", rows, err)
	}
}

func TestStoreOldestHourEmpty(t *testing.T) {
	st := openTestStore(t, filepath.Join(t.TempDir(), dbFileName))
	if _, ok, err := st.oldestHour(context.Background()); ok || err != nil {
		t.Fatalf("empty store oldestHour ok=%v err=%v", ok, err)
	}
}

func TestStoreRetentionDelete(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, filepath.Join(t.TempDir(), dbFileName))
	const day = int64(86400)
	batch := map[Key]Counters{}
	for d := int64(0); d < 30; d++ {
		batch[testKey(d*day, "m")] = Counters{Requests: 1}
	}
	if err := st.upsert(ctx, batch); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	deleted, err := st.cleanupBefore(ctx, 20*day)
	if err != nil {
		t.Fatalf("cleanupBefore: %v", err)
	}
	if deleted != 20 {
		t.Fatalf("deleted = %d, want 20", deleted)
	}
	oldest, _, _ := st.oldestHour(ctx)
	if oldest != 20*day {
		t.Fatalf("oldest = %d, want %d", oldest, 20*day)
	}
	if deleted, err = st.cleanupBefore(ctx, 20*day); err != nil || deleted != 0 {
		t.Fatalf("second cleanup = %d, %v", deleted, err)
	}
}

func TestStorePriceCacheRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t, filepath.Join(t.TempDir(), dbFileName))
	if _, _, _, ok, err := st.loadPriceCache(ctx); ok || err != nil {
		t.Fatalf("empty cache ok=%v err=%v", ok, err)
	}
	at := time.Unix(1_800_000_000, 0)
	if err := st.savePriceCache(ctx, []byte(`{"x":1}`), at, `"etag-1"`); err != nil {
		t.Fatalf("save: %v", err)
	}
	// A 304 refreshes only the fetch time.
	later := at.Add(24 * time.Hour)
	if err := st.savePriceCache(ctx, nil, later, `"etag-1"`); err != nil {
		t.Fatalf("save 304: %v", err)
	}
	body, fetchedAt, etag, ok, err := st.loadPriceCache(ctx)
	if err != nil || !ok {
		t.Fatalf("load ok=%v err=%v", ok, err)
	}
	if string(body) != `{"x":1}` || !fetchedAt.Equal(later) || etag != `"etag-1"` {
		t.Fatalf("cache = %q %v %q", body, fetchedAt, etag)
	}
}

func TestStoreOpensNewerCompatibleSchema(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), dbFileName)
	closeStore(t, openTestStore(t, path))
	db := rawDB(t, path)
	if _, err := db.Exec("PRAGMA user_version = 2"); err != nil {
		t.Fatalf("bump: %v", err)
	}
	_ = db.Close()

	st := openTestStore(t, path)
	if err := st.upsert(ctx, map[Key]Counters{testKey(0, "m"): {Requests: 1}}); err != nil {
		t.Fatalf("write to a newer compatible file: %v", err)
	}
}

func TestStoreRefusesNewerIncompatibleSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), dbFileName)
	closeStore(t, openTestStore(t, path))
	db := rawDB(t, path)
	if _, err := db.Exec("PRAGMA user_version = 2; UPDATE meta SET v = '2' WHERE k = 'write_compat'"); err != nil {
		t.Fatalf("bump: %v", err)
	}
	_ = db.Close()

	_, err := openStore(context.Background(), path)
	var tooNew errTooNew
	if !errors.As(err, &tooNew) {
		t.Fatalf("openStore err = %v, want errTooNew", err)
	}
	if tooNew.writeCompat != 2 {
		t.Fatalf("errTooNew = %+v", tooNew)
	}
}

func TestStoreReplacesACorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, dbFileName)
	if err := os.WriteFile(path, []byte(strings.Repeat("not a database ", 100)), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	st := openTestStore(t, path)
	if err := st.upsert(context.Background(), map[Key]Counters{testKey(0, "m"): {Requests: 1}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	matches, _ := filepath.Glob(path + ".corrupt-*")
	if len(matches) != 1 {
		t.Fatalf("corrupt copies = %v, want one", matches)
	}
}

func TestStoreOpenFailsOnADirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), dbFileName)
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := openStore(context.Background(), path); err == nil {
		t.Fatal("openStore succeeded on a directory")
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("the directory was moved: %v", err)
	}
}
