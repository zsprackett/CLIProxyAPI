package prompt

import "github.com/tidwall/gjson"

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
//   - input: one hash per item over the raw item bytes. A string input is one item.
//   - cacheKey: hash of prompt_cache_key (0 if absent). Not a prefix segment.
//   - incremental: true when "previous_response_id" is present (duplex websocket
//     continuation). The input is then a delta and Diverge compares only tools and
//     instructions.
//   - ttl: TTLUnknown (OpenAI does not let the caller set it here; the executors
//     delete prompt_cache_retention).
func codexPrint(body []byte) *Print {
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		return nil
	}
	p := &Print{format: FormatCodexResponses, ttl: TTLUnknown}
	h := newHasher()

	if tools := root.Get("tools"); tools.IsArray() {
		count := 0
		tools.ForEach(func(_, _ gjson.Result) bool {
			count++
			return true
		})
		p.toolCount = uint16(min(count, 1<<16-1))
		p.hashes = append(p.hashes, h.hashString(tools.Raw))
		p.layout.tools = 1
	}

	p.hashes = append(p.hashes, h.hashString(root.Get("instructions").Str))
	p.layout.instr = 1

	input := root.Get("input")
	switch {
	case input.Type == gjson.String:
		p.hashes = append(p.hashes, h.hashString(input.Raw))
		p.layout.items = 1
	case input.IsArray():
		input.ForEach(func(_, item gjson.Result) bool {
			p.hashes = append(p.hashes, h.hashString(item.Raw))
			p.layout.items++
			return true
		})
	}

	if key := root.Get("prompt_cache_key"); key.Type == gjson.String && key.Str != "" {
		p.cacheKey = h.hashString(key.Str)
	}
	if prev := root.Get("previous_response_id"); prev.Type == gjson.String && prev.Str != "" {
		p.incremental = true
	}
	p.fold()
	return p
}
