package prompt

// codexPrint fingerprints an OpenAI Responses body (Codex HTTP and websocket).
//
// Which body: the FINAL upstream body, i.e. after cacheHelper (HTTP) or
// applyCodexPromptCacheHeadersWithContext (websocket) has set prompt_cache_key,
// and after SanitizeCodexInputItemIDs. Reasons:
//   - prompt_cache_key is a routing hint that decides which cache shard is hit;
//     it only exists after cacheHelper, and "cache key changed" is a real cause.
//   - SanitizeCodexInputItemIDs can DROP encrypted reasoning items
//     (shouldDropCodexEncryptedReasoningItem), which shifts input indexes. Hashing the
//     pre-sanitized body would report "input 12 changed" at an index the upstream
//     never saw.
//
// The "type":"response.create" field that buildCodexWebsocketRequestBody adds is a
// top-level field and is not part of any segment, so hashing either the websocket
// frame or the body before that one sjson.Set gives identical segment hashes.
//
// Section order: tools -> instructions -> input.
//
// Canonical bytes:
//   - tools: raw array bytes, one hash; toolCount = len(tools).
//   - instructions: raw string bytes (absent and "" both hash as empty).
//   - input: one hash per item over the raw item bytes.
//   - cacheKey: hash of prompt_cache_key (0 if absent). Not a prefix segment.
//   - incremental: true when "previous_response_id" is present (duplex websocket
//     continuation). The input is then a delta and Diverge compares only tools and
//     instructions.
//   - ttl: TTLUnknown (OpenAI does not let the caller set it here; the executors
//     delete prompt_cache_retention).
func codexPrint(body []byte) *Print { panic("not implemented") }
