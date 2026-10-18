package requestlog

import (
	"context"

	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

// HandleUsage implements usage.Plugin. It runs on the shared serial usage worker,
// so it does only O(segments) work: no hashing of bodies (that happened at
// publish), no I/O, no allocation beyond interning a new session ID.
//
//	if !s.enabled.Load(): return
//	p := prompt.FromContext(ctx)                       // nil for unsupported providers and side-model records
//	tokens, known := tokensFromDetail(record.Detail)
//	obs := observation{
//	    sessionID:   record.SessionID,
//	    model:       record.Model,
//	    authID:      record.AuthID,
//	    requestedAt: record.RequestedAt (s.now() if zero),
//	    touchedAt:   requestedAt + (record.TTFT if > 0 else record.Latency),
//	    failed:      record.Failed,
//	    tokens:      tokens, tokensKnown: known,
//	    print:       p,
//	}
//	v := s.tracker.observe(obs)                         // tracker.mu only
//	s.ring.append(row{                                  // ring.mu only, never nested with tracker.mu
//	    atMs, requestID (parsed uuid), traceID, sessionID,
//	    keyHash:  quotaforecast.HashClientKey(record.APIKey),   // raw key never stored
//	    authID, provider, model,
//	    agent:    quotaforecast.AgentFamily(internallogging.GetClientRequestMetadata(ctx).UserAgent),
//	    errText:  truncate(record.Fail.Body, 200) when failed,
//	    status:   record.Fail.StatusCode when failed (0 -> 502), else 200,
//	    latency/ttft in ms, tokens clamped to uint32, flags, verdict: v,
//	})
//
// The ctx may already be cancelled; only its values are read.
func (s *Service) HandleUsage(ctx context.Context, record coreusage.Record) {
	panic("not implemented")
}
