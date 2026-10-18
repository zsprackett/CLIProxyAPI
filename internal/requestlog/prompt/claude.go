package prompt

import (
	"strings"

	"github.com/tidwall/gjson"
)

// billingHeaderPrefix starts the per-request system block that CCH signing rewrites.
const billingHeaderPrefix = "x-anthropic-billing-header:"

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
//     never hit otherwise). Its slot keeps the constant volatileSlot so block
//     indexes still match the body, and its hash goes to p.volatile instead, so a
//     miss whose only change is this block can be reported as "volatile_changed"
//     (low confidence) rather than blamed on "system block 0".
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
// The walk uses gjson.ForEach over the raw body and never re-marshals. Raw member
// bytes are hashed as they appear, so any byte that changes on the wire changes
// the hash (key order and whitespace included: if they change upstream, the
// rendered prompt may change too, and a false "changed" beats a silent miss).
func claudePrint(body []byte) *Print {
	root := gjson.ParseBytes(body)
	if !root.IsObject() {
		return nil
	}
	p := &Print{format: FormatClaudeMessages}
	h := newHasher()
	breakpoints, ttl1h := 0, false
	note := func(bp, oneHour bool) {
		if bp {
			breakpoints++
		}
		ttl1h = ttl1h || oneHour
	}

	if tools := root.Get("tools"); tools.IsArray() {
		h.reset()
		count := 0
		tools.ForEach(func(_, tool gjson.Result) bool {
			count++
			note(h.writeMembers(tool))
			h.sep()
			return true
		})
		p.toolCount = uint16(min(count, 1<<16-1))
		p.hashes = append(p.hashes, h.sum())
		p.layout.tools = 1
	}

	system := root.Get("system")
	switch {
	case system.Type == gjson.String:
		p.hashes = append(p.hashes, h.hashString(system.Str))
		p.layout.system = 1
	case system.IsArray():
		system.ForEach(func(_, block gjson.Result) bool {
			h.reset()
			note(h.writeMembers(block))
			if isBillingHeaderBlock(block.Get("text").Str) {
				p.volatile ^= h.sum()
				p.hashes = append(p.hashes, volatileSlot)
			} else {
				p.hashes = append(p.hashes, h.sum())
			}
			p.layout.system++
			return true
		})
	}

	h.reset()
	h.write(root.Get("tool_choice").Raw)
	h.sep()
	h.write(root.Get("thinking").Raw)
	p.hashes = append(p.hashes, h.sum())
	p.layout.settings = 1

	blocks := 0
	root.Get("messages").ForEach(func(_, msg gjson.Result) bool {
		h.reset()
		h.write(msg.Get("role").Raw)
		h.sep()
		content := msg.Get("content")
		switch {
		case content.Type == gjson.String:
			h.write(content.Raw)
			blocks++
		case content.IsArray():
			content.ForEach(func(_, block gjson.Result) bool {
				note(h.writeMembers(block))
				h.sep()
				blocks++
				return true
			})
		}
		p.hashes = append(p.hashes, h.sum())
		p.layout.items++
		return true
	})

	p.blocks = uint32(min(blocks, 1<<32-1))
	p.breakpoints = uint8(min(breakpoints, 255))
	switch {
	case ttl1h:
		p.ttl = TTL1h
	case breakpoints > 0:
		p.ttl = TTL5m
	default:
		p.ttl = TTLNone
	}
	p.fold()
	return p
}

// isBillingHeaderBlock reports whether a system block is the per-request
// x-anthropic-billing-header block.
func isBillingHeaderBlock(text string) bool {
	return strings.HasPrefix(text, billingHeaderPrefix)
}
