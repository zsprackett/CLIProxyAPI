//go:build !solaris

package usagehistory

// modernc.org/sqlite is pure Go, so CGO_ENABLED=0 release builds keep working.
// It does not support Solaris. There, no driver is registered, the store fails
// to open, and the service records usage in memory only.
import _ "modernc.org/sqlite"
