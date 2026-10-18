package helps

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/requestlog/prompt"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

const promptTestProvider = "requestlog-prompt-test"

type capturedRecord struct {
	record usage.Record
	print  *prompt.Print
}

type promptCapture struct{ records chan capturedRecord }

func (p *promptCapture) HandleUsage(ctx context.Context, record usage.Record) {
	if record.Provider == promptTestProvider {
		p.records <- capturedRecord{record: record, print: prompt.FromContext(ctx)}
	}
}

var promptCapturePlugin = func() *promptCapture {
	capture := &promptCapture{records: make(chan capturedRecord, 8)}
	usage.RegisterNamedPlugin("requestlog-prompt-test-capture", capture)
	return capture
}()

func nextCaptured(t *testing.T) capturedRecord {
	t.Helper()
	select {
	case rec := <-promptCapturePlugin.records:
		return rec
	case <-time.After(3 * time.Second):
		t.Fatal("usage record was not dispatched")
	}
	return capturedRecord{}
}

var claudeTestBody = []byte(`{"model":"m","system":"be brief","messages":[{"role":"user","content":"hi"}]}`)

func TestCapturePromptAttachesPrintAtPublish(t *testing.T) {
	prompt.SetCaptureEnabled(true)
	defer prompt.SetCaptureEnabled(false)
	reporter := NewUsageReporter(context.Background(), promptTestProvider, "m", nil)
	reporter.CapturePrompt(claudeTestBody, "claude")
	if reporter.promptBody == nil || reporter.promptFormat != prompt.FormatClaudeMessages {
		t.Fatal("CapturePrompt must keep a reference to the body while enabled")
	}
	reporter.Publish(context.Background(), usage.Detail{InputTokens: 5})
	rec := nextCaptured(t)
	if rec.print == nil || rec.print.Format() != prompt.FormatClaudeMessages || rec.print.Size() != 3 {
		t.Fatalf("attempt record must carry the print: %+v", rec.print)
	}
	if reporter.promptBody != nil {
		t.Fatal("body must be released after publish")
	}
}

func TestCapturePromptKeepsNothingWhenDisabled(t *testing.T) {
	prompt.SetCaptureEnabled(false)
	reporter := NewUsageReporter(context.Background(), promptTestProvider, "m", nil)
	reporter.CapturePrompt(claudeTestBody, "claude")
	if reporter.promptBody != nil {
		t.Fatal("disabled capture must not hold the body")
	}
	reporter.Publish(context.Background(), usage.Detail{InputTokens: 5})
	if rec := nextCaptured(t); rec.print != nil {
		t.Fatal("disabled capture must not attach a print")
	}
}

func TestCapturePromptSkipsFailedAttempts(t *testing.T) {
	prompt.SetCaptureEnabled(true)
	defer prompt.SetCaptureEnabled(false)
	reporter := NewUsageReporter(context.Background(), promptTestProvider, "m", nil)
	reporter.CapturePrompt(claudeTestBody, "claude")
	reporter.PublishFailure(context.Background(), errors.New("boom"))
	rec := nextCaptured(t)
	if !rec.record.Failed || rec.print != nil {
		t.Fatalf("failed attempt must publish without a print: failed=%v print=%v", rec.record.Failed, rec.print)
	}
	if reporter.promptBody != nil {
		t.Fatal("body must be released on failure too")
	}
}

func TestCapturePromptSideModelRecordsCarryNoPrint(t *testing.T) {
	prompt.SetCaptureEnabled(true)
	defer prompt.SetCaptureEnabled(false)
	reporter := NewUsageReporter(context.Background(), promptTestProvider, "m", nil)
	reporter.CapturePrompt(claudeTestBody, "claude")
	reporter.PublishAdditionalModel(context.Background(), "side-model", usage.Detail{InputTokens: 1})
	if rec := nextCaptured(t); rec.record.Model != "side-model" || rec.print != nil {
		t.Fatalf("side-model record = %q print=%v", rec.record.Model, rec.print)
	}
	reporter.Publish(context.Background(), usage.Detail{InputTokens: 5})
	if rec := nextCaptured(t); rec.record.Model != "m" || rec.print == nil {
		t.Fatalf("attempt record = %q print=%v", rec.record.Model, rec.print)
	}
}

func TestCapturePromptUnknownFormatAndNilReporter(t *testing.T) {
	prompt.SetCaptureEnabled(true)
	defer prompt.SetCaptureEnabled(false)
	var nilReporter *UsageReporter
	nilReporter.CapturePrompt(claudeTestBody, "claude")
	reporter := NewUsageReporter(context.Background(), promptTestProvider, "m", nil)
	reporter.CapturePrompt(claudeTestBody, "gemini")
	reporter.EnsurePublished(context.Background())
	if rec := nextCaptured(t); rec.print != nil {
		t.Fatal("unsupported formats must not produce a print")
	}
}
