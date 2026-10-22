package requestlog

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	internallogging "github.com/router-for-me/CLIProxyAPI/v8/internal/logging"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/requestlog/prompt"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagehistory"
	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

// maxErrText bounds the failure text kept per failed row.
const maxErrText = 200

// HandleUsage implements usage.Plugin. It runs on the shared serial usage worker,
// so it does only O(segments) work: no hashing of bodies (that happened at
// publish), no I/O, no allocation beyond interning a new session ID.
// The ctx may already be cancelled; only its values are read.
func (s *Service) HandleUsage(ctx context.Context, record coreusage.Record) {
	if s == nil || !s.enabled.Load() {
		return
	}
	requestedAt := record.RequestedAt
	if requestedAt.IsZero() {
		requestedAt = s.now()
	}
	touched := record.TTFT
	if touched <= 0 {
		touched = record.Latency
	}
	model := strings.TrimSpace(record.Model)
	if model == "" {
		model = strings.TrimSpace(record.Alias)
	}
	tokens, known := tokensFromDetail(record.Detail)
	verdict := s.tracker.observe(observation{
		sessionID:   record.SessionID,
		model:       model,
		authID:      record.AuthID,
		requestedAt: requestedAt,
		touchedAt:   requestedAt.Add(max(touched, 0)),
		failed:      record.Failed,
		tokens:      tokens,
		tokensKnown: known,
		print:       prompt.FromContext(ctx),
	})

	r := row{
		atMs:      requestedAt.UnixMilli(),
		traceID:   strings.TrimSpace(record.TraceID),
		sessionID: record.SessionID,
		keyHash:   usagehistory.HashClientKey(record.APIKey),
		authID:    record.AuthID,
		provider:  strings.ToLower(strings.TrimSpace(record.Provider)),
		model:     model,
		agent:     usagehistory.AgentFamily(internallogging.GetClientRequestMetadata(ctx).UserAgent),
		latencyMs: clampU32(int64(record.Latency / time.Millisecond)),
		ttftMs:    clampU32(int64(record.TTFT / time.Millisecond)),
		uncached:  clampU32(tokens.uncached),
		read:      clampU32(tokens.read),
		write:     clampU32(tokens.write),
		output:    clampU32(tokens.output),
		reasoning: clampU32(tokens.reasoning),
		status:    http.StatusOK,
		verdict:   verdict,
	}
	if id, err := uuid.Parse(record.RequestID); err == nil {
		r.requestID = id
	}
	if record.Stream {
		r.flags |= rowStream
	}
	if record.Failed {
		r.flags |= rowFailed
		r.status = http.StatusBadGateway
		if record.Fail.StatusCode > 0 {
			r.status = int16(record.Fail.StatusCode)
		}
		r.errText = record.Fail.Body
		if len(r.errText) > maxErrText {
			r.errText = strings.ToValidUTF8(r.errText[:maxErrText], "")
		}
	}
	s.ring.append(r)
}
