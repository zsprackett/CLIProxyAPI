package executor

import cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"

// The registered executors for each quota-polled provider must implement QuotaPoller,
// or the manager silently skips them.
var (
	_ cliproxyauth.QuotaPoller = (*ClaudeExecutor)(nil)
	_ cliproxyauth.QuotaPoller = (*CodexAutoExecutor)(nil)
	_ cliproxyauth.QuotaPoller = (*XAIAutoExecutor)(nil)
)
