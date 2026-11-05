package auth

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// SyncCooldownStatesFromStore rebuilds runtime cooldown state from the persisted
// records. It is meant for a node that does not execute requests, such as an
// auth-shard manager: the records written by the owning workers are its only source
// of runtime status, so records that expired or were removed clear the state they set.
// It never writes to the store.
func (m *Manager) SyncCooldownStatesFromStore(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.RLock()
	store := m.cooldownStore
	m.mu.RUnlock()
	if store == nil {
		return nil
	}
	records, errLoad := store.Load(ctx)
	if errLoad != nil {
		return errLoad
	}
	byAuth := make(map[string][]CooldownStateRecord, len(records))
	for _, record := range records {
		if authID := strings.TrimSpace(record.AuthID); authID != "" {
			byAuth[authID] = append(byAuth[authID], record)
		}
	}

	now := time.Now()
	changed := make([]*Auth, 0)
	m.mu.Lock()
	for authID, auth := range m.auths {
		if auth == nil || auth.Disabled || auth.Status == StatusDisabled {
			continue
		}
		before := m.cooldownSyncViewLocked(auth, now)
		clearCooldownDerivedState(auth)
		ApplyLoginRequiredFromMetadata(auth)
		// Model records first so auth-level records win, matching RestoreCooldownStates.
		for _, record := range byAuth[authID] {
			if strings.TrimSpace(record.Model) != "" {
				m.restoreCooldownRecordLocked(record, now)
			}
		}
		for _, record := range byAuth[authID] {
			if strings.TrimSpace(record.Model) == "" {
				m.restoreCooldownRecordLocked(record, now)
			}
		}
		if !before.equal(m.cooldownSyncViewLocked(auth, now)) {
			auth.UpdatedAt = now
			changed = append(changed, auth.Clone())
		}
	}
	m.mu.Unlock()

	if m.scheduler != nil {
		for _, snapshot := range changed {
			m.scheduler.upsertAuth(snapshot)
		}
	}
	return nil
}

// clearCooldownDerivedState resets the runtime failure state that cooldown records
// set, so the following restore reflects only the records that still exist.
// Model states disabled by configuration are kept.
func clearCooldownDerivedState(auth *Auth) {
	for model, state := range auth.ModelStates {
		if state == nil || state.Status != StatusDisabled {
			delete(auth.ModelStates, model)
		}
	}
	if len(auth.ModelStates) == 0 {
		auth.ModelStates = nil
	}
	auth.Unavailable = false
	auth.NextRetryAfter = time.Time{}
	auth.Quota = QuotaState{}
	auth.Status = StatusActive
	auth.StatusMessage = ""
	auth.LastError = nil
}

// cooldownSyncView is the part of an auth that a cooldown sync can change.
type cooldownSyncView struct {
	status      Status
	message     string
	unavailable bool
	nextRetry   time.Time
	lastError   string
	records     []CooldownStateRecord
}

func (m *Manager) cooldownSyncViewLocked(auth *Auth, now time.Time) cooldownSyncView {
	view := cooldownSyncView{
		status:      auth.Status,
		message:     auth.StatusMessage,
		unavailable: auth.Unavailable,
		nextRetry:   auth.NextRetryAfter,
		records:     m.cooldownStateRecordsForAuthLocked(auth, now),
	}
	if auth.LastError != nil {
		view.lastError = fmt.Sprintf("%s|%s|%d", auth.LastError.Code, auth.LastError.Message, auth.LastError.HTTPStatus)
	}
	return view
}

func (v cooldownSyncView) equal(other cooldownSyncView) bool {
	return v.status == other.status &&
		v.message == other.message &&
		v.unavailable == other.unavailable &&
		v.nextRetry.Equal(other.nextRetry) &&
		v.lastError == other.lastError &&
		cooldownStateRecordsEqual(v.records, other.records)
}
