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

// pollKeepsModelCooldown reports whether a poll that shows account capacity
// must still keep the own cooldown of model, because the provider rule says
// the cause of that cooldown has not recovered.
func pollKeepsModelCooldown(obs QuotaObservation, auth *Auth, model string, own *ModelState, now time.Time) bool {
	row, ok := quotaProviders[obs.Provider]
	if !ok || row.modelCooldownHolds == nil || own == nil || !quotaCooldownActive(own.Quota, now) {
		return false
	}
	return row.modelCooldownHolds(obs, QuotaWindowsForAuth(auth), model, own.Quota)
}

// ownModelCooldownLayer returns the layer that holds the model's own failures:
// the state itself, or the independent shadow under a propagated credential
// quota. It is nil when only the propagated layer exists.
func ownModelCooldownLayer(state *ModelState) *ModelState {
	if state == nil {
		return nil
	}
	if state.credentialQuotaPropagated || state.Quota.Reason == "credential_quota" {
		return state.credentialQuotaShadow
	}
	return state
}

// modelCooldownKeeper reports whether a reset keeps the own cooldown layer of
// a model. It runs under m.mu.
type modelCooldownKeeper func(auth *Auth, model string, own *ModelState) bool

// hasClearableQuotaCooldown is hasActiveQuotaCooldown without the model
// cooldowns that keep reports as kept. An auth-level "quota" reason is the
// aggregate of the model cooldowns when any model has one, so the models
// decide.
func hasClearableQuotaCooldown(auth *Auth, now time.Time, keep modelCooldownKeeper) bool {
	if auth == nil {
		return false
	}
	modelBlocked := false
	for model, state := range auth.ModelStates {
		if state == nil || !quotaCooldownActive(state.Quota, now) {
			continue
		}
		modelBlocked = true
		if own := ownModelCooldownLayer(state); own != state || !keep(auth, model, own) {
			return true
		}
	}
	if !quotaCooldownActive(auth.Quota, now) {
		return false
	}
	return auth.Quota.Reason == "credential_quota" || !modelBlocked
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
