// Command trim-prices reads LiteLLM's model_prices_and_context_window.json on
// stdin and writes the trimmed catalog on stdout. scripts/update-model-prices.sh
// uses it to refresh the embedded snapshot of internal/usagehistory/prices.
package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory/prices"
)

func main() {
	if err := run(os.Stdin, os.Stdout, time.Now()); err != nil {
		fmt.Fprintf(os.Stderr, "trim-prices: %v\n", err)
		os.Exit(1)
	}
}

func run(in io.Reader, out io.Writer, now time.Time) error {
	raw, err := io.ReadAll(in)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	catalog, err := prices.Trim(raw, prices.SourceBuiltin, now)
	if err != nil {
		return err
	}
	body, err := catalog.Encode()
	if err != nil {
		return err
	}
	if _, err = out.Write(body); err != nil {
		return fmt.Errorf("write stdout: %w", err)
	}
	return nil
}
