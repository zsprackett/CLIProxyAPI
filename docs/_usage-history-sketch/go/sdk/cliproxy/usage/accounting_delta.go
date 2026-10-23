package usage

// Delta to sdk/cliproxy/usage/{accounting.go,manager.go}: the 5m/1h cache-write split.
//
// Shape: the 1h count is a SUBSET of the existing cache-write count, not a new
// sibling bucket. Every existing consumer (requestlog verdicts, quotaforecast,
// redis queue, Home forwarding, translators' tests) keeps reading
// Input.CacheWriteTokens as "all cache writes" and is untouched. Only consumers
// that care (usagehistory) read the subset. TokenAccountingSchemaVersion stays 2:
// the field is additive and omitempty, so old JSON decodes with 1h = 0
// (= "all writes at the default TTL"), which is exactly what it meant before.

// TokenInputBreakdown (accounting.go) gains one field:
//
//	type TokenInputBreakdown struct {
//		TotalTokens      int64 `json:"total_tokens"`
//		UncachedTokens   int64 `json:"uncached_tokens"`
//		CacheReadTokens  int64 `json:"cache_read_tokens"`
//		CacheWriteTokens int64 `json:"cache_write_tokens"`
//		// CacheWrite1hTokens is the part of CacheWriteTokens written with a 1-hour
//		// TTL (Anthropic cache_creation.ephemeral_1h_input_tokens). The rest of
//		// CacheWriteTokens is default-TTL (5-minute) writes. 0 for other providers.
//		CacheWrite1hTokens int64 `json:"cache_write_1h_tokens,omitempty"`
//	}
//
// Valid() gains one clause (invariant enforced where every consumer already checks):
//
//	if b.Input.CacheWrite1hTokens < 0 || b.Input.CacheWrite1hTokens > b.Input.CacheWriteTokens { return false }

// Detail (manager.go) gains the flat legacy counterpart, needed because
// MergeStreamUsageDetail and EnsureTokenBreakdownForProvider rebuild the
// breakdown from Detail's flat fields:
//
//	// CacheCreation1hTokens is the 1-hour-TTL part of CacheCreationTokens.
//	CacheCreation1hTokens int64

// WithCacheWrite1h returns b with the 1h subset set. The positional constructors
// (NewIndependentTokenBreakdown & co.) are NOT widened: only the Claude paths
// know the split, so they chain this. Out-of-range n (negative, or more than the
// cache writes) leaves the subset 0: the writes still count, at the 5m price,
// and Valid() stays true.
func (b TokenBreakdown) WithCacheWrite1h(n int64) TokenBreakdown { panic("not implemented") }

// EnsureTokenBreakdownForProvider (accounting.go) change, one line after
// building the breakdown from flat fields:
//
//	detail.TokenBreakdown = tokenBreakdownForSemantics(detail, semantics).WithCacheWrite1h(detail.CacheCreation1hTokens)
