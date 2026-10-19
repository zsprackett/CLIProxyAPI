package auth

import (
	"time"
)

// pollShowsQuotaCapacity reports whether a polled observation proves the
// account has room again. The rule is the provider's showsCapacity row,
// evaluated on the poll result alone, never on the merged snapshot.
func pollShowsQuotaCapacity(obs QuotaObservation) bool {
	row, ok := quotaProviders[obs.Provider]
	if !ok || row.showsCapacity == nil {
		return false
	}
	return row.showsCapacity(obs)
}

// hasActiveQuotaCooldown reports whether the auth, or any of its model states,
// is blocked by a quota cooldown whose deadline is still ahead.
func hasActiveQuotaCooldown(auth *Auth, now time.Time) bool {
	if auth == nil {
		return false
	}
	if quotaCooldownActive(auth.Quota, now) {
		return true
	}
	for _, state := range auth.ModelStates {
		if state != nil && quotaCooldownActive(state.Quota, now) {
			return true
		}
	}
	return false
}

// cooldownRecordsUnchanged compares cooldown records while ignoring UpdatedAt
// on the auth-level record. The poll's own observation bumps auth.UpdatedAt.
// Model records keep their UpdatedAt, which every failure advances.
func cooldownRecordsUnchanged(before, after []CooldownStateRecord) bool {
	strip := func(records []CooldownStateRecord) []CooldownStateRecord {
		out := make([]CooldownStateRecord, len(records))
		copy(out, records)
		for i := range out {
			if out[i].Model == "" {
				out[i].UpdatedAt = time.Time{}
			}
		}
		return out
	}
	return cooldownStateRecordsEqual(strip(before), strip(after))
}

func quotaCooldownActive(quota QuotaState, now time.Time) bool {
	return quota.Exceeded &&
		(quota.Reason == "credential_quota" || quota.Reason == "quota") &&
		quota.NextRecoverAt.After(now)
}
