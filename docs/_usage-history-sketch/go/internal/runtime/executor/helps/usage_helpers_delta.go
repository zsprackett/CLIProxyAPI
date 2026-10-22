package helps

// Delta to internal/runtime/executor/helps/usage_helpers.go and plugin_executor_usage.go.
// No change to internal/translator (records come from the executor-side parse).
//
// Flow of the 5m/1h split, end to end:
//
//	Anthropic usage JSON
//	  usage.cache_creation_input_tokens                    (5m + 1h)
//	  usage.cache_creation.ephemeral_5m_input_tokens
//	  usage.cache_creation.ephemeral_1h_input_tokens
//	    |
//	    v  parseClaudeUsageNode (non-stream, stream message_start / message_delta, plugin executor)
//	usage.Detail{CacheCreationTokens: total, CacheCreation1hTokens: 1h}
//	usage.TokenBreakdown{Input.CacheWriteTokens: total, Input.CacheWrite1hTokens: 1h}
//	    |
//	    v  MergeStreamUsageDetail (stream: message_start carries cache fields, message_delta often 0)
//	merged.CacheCreation1hTokens kept from existing when update has 0, like CacheCreationTokens
//	    |
//	    v  usage.Record.Detail  ->  Manager queue  ->  usagehistory.HandleUsage
//	rollupOf: CacheWrite1h = Input.CacheWrite1hTokens; CacheWrite5m = Input.CacheWriteTokens - CacheWrite1h
//	    |
//	    v  rollup_hour.cache_write_5m / cache_write_1h  ->  priced at read time with
//	       cache_creation_input_token_cost / cache_creation_input_token_cost_above_1hr

// parseClaudeUsageNode change (pseudocode, ~L1176):
//
//	cacheCreationTokens := usageNode.Get("cache_creation_input_tokens").Int()
//	eph5m := usageNode.Get("cache_creation.ephemeral_5m_input_tokens")
//	eph1h := usageNode.Get("cache_creation.ephemeral_1h_input_tokens")
//	if !usageNode.Get("cache_creation_input_tokens").Exists() && (eph5m.Exists() || eph1h.Exists()) {
//	    cacheCreationTokens = eph5m.Int() + eph1h.Int()    // gateways that only send the object
//	}
//	cacheCreation1h := max(eph1h.Int(), 0)
//	detail.CacheCreation1hTokens = cacheCreation1h
//	detail.TokenBreakdown = usage.NewIndependentTokenBreakdown(...unchanged...).WithCacheWrite1h(cacheCreation1h)
//
// MergeStreamUsageDetail change (pseudocode, plugin_executor_usage.go ~L144):
//
//	if merged.CacheCreation1hTokens == 0 && existing.CacheCreation1hTokens > 0 {
//	    merged.CacheCreation1hTokens = existing.CacheCreation1hTokens
//	}
//	merged.TokenBreakdown = usage.NewIndependentTokenBreakdown(...unchanged...).WithCacheWrite1h(merged.CacheCreation1hTokens)
//
// Tests (usage_helpers_test.go): a message_start with cache_creation {5m: 100, 1h: 900}
// and cache_creation_input_tokens 1000 followed by a message_delta with only
// output_tokens yields CacheWriteTokens 1000 / CacheWrite1hTokens 900; a payload with
// 1h > total yields 1h = 0 and Valid() true; a payload without the object yields 0.
