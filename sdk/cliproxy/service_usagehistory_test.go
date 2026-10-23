package cliproxy

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

// A record published after the HTTP server stopped, and drained by the usage
// queue during Shutdown, must reach the usage history file. That holds only if
// Shutdown stops usagehistory after the drain, and if usagehistory keeps running
// after Run cancelled its context.
func TestShutdownWritesLateUsageToHistory(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{AuthDir: dir}
	cfg.UsageHistory.Enabled = true
	cfg.UsageHistory.Prices.DisableRemote = true
	manager := coreauth.NewManager(nil, nil, nil)
	manager.SetConfig(cfg)

	runCtx, cancelRun := context.WithCancel(context.Background())
	usagehistory.Default().Start(runCtx, manager)

	queue := usage.NewManager(16)
	queue.Register(usagehistory.Default())
	queue.Start(context.Background())
	saved := stopUsageQueue
	stopUsageQueue = func() {
		queue.Publish(context.Background(), usage.Record{
			Provider: "claude", AuthID: "claude-a.json", Model: "late-record", RequestedAt: time.Now(),
			Detail: usage.Detail{InputTokens: 1, OutputTokens: 2},
		})
		queue.Stop()
	}
	t.Cleanup(func() { stopUsageQueue = saved })

	// Run cancels its context before its deferred Shutdown runs.
	cancelRun()
	if err := (&Service{cfg: cfg}).Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, ".usage-history.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() {
		if errClose := db.Close(); errClose != nil {
			t.Errorf("close: %v", errClose)
		}
	}()
	var requests sql.NullInt64
	if err = db.QueryRow("SELECT SUM(requests) FROM rollup_hour WHERE model = 'late-record'").Scan(&requests); err != nil {
		t.Fatalf("query: %v", err)
	}
	if requests.Int64 != 1 {
		t.Fatalf("late record rows = %v, want 1", requests)
	}
}
