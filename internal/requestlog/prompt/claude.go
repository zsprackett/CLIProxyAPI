package prompt

// claudePrint fingerprints an Anthropic Messages body exactly as it goes upstream
// (after ensureCacheControl, TTL upgrade, CCH signing, sanitizing).
//
// Section order follows Anthropic's cache evaluation order:
//
//	tools -> system -> settings -> messages
//
// Canonical bytes per segment (hashed with sha256, first 8 bytes kept):
//
//   - tools: for each tool object, every top-level member EXCEPT "cache_control",
//     written as key + raw value bytes in body order. One hash for the whole
//     array; toolCount = len(tools). Absent tools -> no tools hash (layout.tools=0).
//
//   - system: if "system" is a string -> one block whose bytes are the string.
//     If an array -> one hash per block, members except "cache_control".
//     A block whose text starts with "x-anthropic-billing-header:" is NOT hashed
//     into the prefix: its cc_version/cch change on every request (claude_signing.go)
//     and Anthropic evidently does not key the cache on it (native Claude Code would
//     never hit otherwise). Its hash goes to p.volatile instead, so a miss whose
//     only change is this block can be reported as "volatile_changed" (low confidence)
//     rather than blamed on "system block 0".
//
//   - settings: one hash over raw "tool_choice" and raw "thinking" (absent -> empty).
//     Anthropic documents that changing either invalidates the message cache while
//     tools and system stay cached, so it sits between system and messages.
//
//   - messages: one hash per message over "role" plus content. String content ->
//     the string bytes. Array content -> for each block, members except
//     "cache_control". blocks += number of content blocks (string content counts 1).
//     The moving last-message breakpoint therefore never shows as a change.
//     The injected currentDate reminder IS hashed: when the date rolls over, message 0
//     really changes and the report says so.
//
// Also collected on the same pass:
//   - breakpoints: count of cache_control objects in tools, system, messages.
//   - ttl: TTL1h if any breakpoint has ttl "1h", else TTL5m if any breakpoint exists,
//     else TTLNone.
//
// Implementation note: walk with gjson.ForEach over the raw body; never re-marshal.
// Hash raw member bytes as they appear, so any byte that changes on the wire changes
// the hash (key order and whitespace included: if they change upstream, the
// rendered prompt may change too, and we prefer a false "changed" to a silent miss).
func claudePrint(body []byte) *Print { panic("not implemented") }

// isBillingHeaderBlock reports whether a system block is the per-request
// x-anthropic-billing-header block.
func isBillingHeaderBlock(text string) bool { panic("not implemented") }
