package usagehistory

import (
	"database/sql"
	"testing"
)

func TestSQLiteDriverOpensADatabase(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() {
		if errClose := db.Close(); errClose != nil {
			t.Errorf("close: %v", errClose)
		}
	}()
	var version string
	if errQuery := db.QueryRow("SELECT sqlite_version()").Scan(&version); errQuery != nil {
		t.Fatalf("query: %v", errQuery)
	}
	if version == "" {
		t.Fatal("empty sqlite version")
	}
}
