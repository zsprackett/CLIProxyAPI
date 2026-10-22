package helps

import "testing"

func TestParseClaudeUsageSplitsOneHourCacheWrites(t *testing.T) {
	data := []byte(`{"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":20,"cache_creation_input_tokens":1000,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":900}}}`)
	detail := ParseClaudeUsage(data)
	if detail.CacheCreationTokens != 1000 || detail.CacheCreation1hTokens != 900 {
		t.Fatalf("detail cache creation = %d (1h %d), want 1000 (1h 900)", detail.CacheCreationTokens, detail.CacheCreation1hTokens)
	}
	breakdown := detail.TokenBreakdown
	if !breakdown.Valid() || breakdown.Input.CacheWriteTokens != 1000 || breakdown.Input.CacheWrite1hTokens != 900 {
		t.Fatalf("token breakdown = %+v", breakdown)
	}
	if detail.TotalTokens != 1035 {
		t.Fatalf("total tokens = %d, want 1035", detail.TotalTokens)
	}
}

func TestParseClaudeUsageWithoutCacheCreationObjectHasNoOneHourWrites(t *testing.T) {
	data := []byte(`{"usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":1000}}`)
	detail := ParseClaudeUsage(data)
	if detail.CacheCreation1hTokens != 0 || detail.TokenBreakdown.Input.CacheWrite1hTokens != 0 {
		t.Fatalf("1h writes = %d / %d, want 0", detail.CacheCreation1hTokens, detail.TokenBreakdown.Input.CacheWrite1hTokens)
	}
	if !detail.TokenBreakdown.Valid() || detail.TokenBreakdown.Input.CacheWriteTokens != 1000 {
		t.Fatalf("token breakdown = %+v", detail.TokenBreakdown)
	}
}

func TestParseClaudeUsageUsesCacheCreationObjectWhenTotalIsMissing(t *testing.T) {
	data := []byte(`{"usage":{"input_tokens":10,"output_tokens":5,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":900}}}`)
	detail := ParseClaudeUsage(data)
	if detail.CacheCreationTokens != 1000 || detail.CacheCreation1hTokens != 900 {
		t.Fatalf("detail cache creation = %d (1h %d), want 1000 (1h 900)", detail.CacheCreationTokens, detail.CacheCreation1hTokens)
	}
	if !detail.TokenBreakdown.Valid() || detail.TokenBreakdown.Input.CacheWrite1hTokens != 900 || detail.TotalTokens != 1015 {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestParseClaudeUsageDropsOneHourWritesAboveTotal(t *testing.T) {
	data := []byte(`{"usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":100,"cache_creation":{"ephemeral_1h_input_tokens":900}}}`)
	detail := ParseClaudeUsage(data)
	if detail.TokenBreakdown.Input.CacheWrite1hTokens != 0 {
		t.Fatalf("1h subset = %d, want 0", detail.TokenBreakdown.Input.CacheWrite1hTokens)
	}
	if !detail.TokenBreakdown.Valid() || detail.TokenBreakdown.Input.CacheWriteTokens != 100 {
		t.Fatalf("token breakdown = %+v", detail.TokenBreakdown)
	}
}

func TestStreamUsageBufferObserveClaudeStreamKeepsOneHourWritesFromMessageStart(t *testing.T) {
	var buffer StreamUsageBuffer
	buffer.ObserveClaudeStream([]byte(`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-opus-5-5","usage":{"input_tokens":10,"cache_creation_input_tokens":1000,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":900},"cache_read_input_tokens":20,"output_tokens":1}}}`))
	buffer.ObserveClaudeStream([]byte(`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":15}}`))

	detail, ok := buffer.Detail()
	if !ok {
		t.Fatal("expected buffer to contain usage detail")
	}
	if detail.CacheCreationTokens != 1000 || detail.CacheCreation1hTokens != 900 {
		t.Fatalf("detail cache creation = %d (1h %d), want 1000 (1h 900)", detail.CacheCreationTokens, detail.CacheCreation1hTokens)
	}
	breakdown := detail.TokenBreakdown
	if !breakdown.Valid() || breakdown.Input.CacheWriteTokens != 1000 || breakdown.Input.CacheWrite1hTokens != 900 {
		t.Fatalf("token breakdown = %+v", breakdown)
	}
}

func TestStreamUsageBufferObserveClaudeStreamKeepsOneHourWritesWhenDeltaRepeatsTotal(t *testing.T) {
	var buffer StreamUsageBuffer
	buffer.ObserveClaudeStream([]byte(`data: {"type":"message_start","message":{"usage":{"input_tokens":10,"cache_creation_input_tokens":1000,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":900},"output_tokens":1}}}`))
	buffer.ObserveClaudeStream([]byte(`data: {"type":"message_delta","usage":{"input_tokens":10,"cache_creation_input_tokens":1000,"output_tokens":15}}`))

	detail, ok := buffer.Detail()
	if !ok {
		t.Fatal("expected buffer to contain usage detail")
	}
	if detail.TokenBreakdown.Input.CacheWrite1hTokens != 900 || !detail.TokenBreakdown.Valid() {
		t.Fatalf("token breakdown = %+v", detail.TokenBreakdown)
	}
}
