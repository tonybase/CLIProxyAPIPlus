package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/authstatus"
)

func newCooldownSyncManager(t *testing.T, store *recordingCooldownStateStore, auth *Auth) *Manager {
	t.Helper()
	m := NewManager(nil, nil, nil)
	m.SetCooldownStateStore(store)
	if _, err := m.Register(WithSkipPersist(context.Background()), auth); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRestoreCooldownStates_ModelRecordSurfacesAuthLevelReason(t *testing.T) {
	store := &recordingCooldownStateStore{}
	store.load = []CooldownStateRecord{{
		AuthID:         "kiro-restore.json",
		Model:          "claude-sonnet-4",
		Status:         "cooling",
		Reason:         "payment_required",
		NextRetryAfter: time.Now().Add(time.Hour),
		LastError:      &Error{Message: "You have reached the limit.", HTTPStatus: http.StatusPaymentRequired},
	}}
	m := newCooldownSyncManager(t, store, fileAuth("kiro-restore.json"))

	if err := m.RestoreCooldownStates(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, _ := m.GetByID("kiro-restore.json")
	if !current.Unavailable || current.Status != StatusError {
		t.Fatalf("restored model cooldown should mark the auth: unavailable=%v status=%s", current.Unavailable, current.Status)
	}
	if current.StatusMessage != "payment_required" || current.LastError == nil {
		t.Fatalf("restored model cooldown should expose its reason: msg=%q err=%v", current.StatusMessage, current.LastError)
	}
}

func TestSyncCooldownStatesFromStore_FollowsPeerRecords(t *testing.T) {
	store := &recordingCooldownStateStore{}
	m := newCooldownSyncManager(t, store, fileAuth("kiro-sync.json"))

	store.mu.Lock()
	store.load = []CooldownStateRecord{{
		AuthID:         "kiro-sync.json",
		Model:          "claude-sonnet-4",
		Status:         "cooling",
		Reason:         "unauthorized",
		NextRetryAfter: time.Now().Add(time.Hour),
		LastError:      &Error{Code: "unauthorized", Message: "bearer token invalid", HTTPStatus: http.StatusUnauthorized},
	}}
	store.mu.Unlock()
	if err := m.SyncCooldownStatesFromStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, _ := m.GetByID("kiro-sync.json")
	if !current.Unavailable || current.Status != StatusError || current.StatusMessage != "unauthorized" {
		t.Fatalf("sync should mirror the peer cooldown: unavailable=%v status=%s msg=%q",
			current.Unavailable, current.Status, current.StatusMessage)
	}

	// The worker cleared the cooldown and removed its state file.
	store.mu.Lock()
	store.load = nil
	store.mu.Unlock()
	if err := m.SyncCooldownStatesFromStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, _ = m.GetByID("kiro-sync.json")
	if current.Unavailable || current.Status != StatusActive || current.StatusMessage != "" || len(current.ModelStates) != 0 {
		t.Fatalf("removed record should clear the mirrored state: %+v", current)
	}
	if got := store.saveCount.Load(); got != 0 {
		t.Fatalf("sync must never write cooldown state, got %d saves", got)
	}
}

func TestSyncCooldownStatesFromStore_DropsExpiredRecordsKeepsLoginRequired(t *testing.T) {
	store := &recordingCooldownStateStore{}
	auth := fileAuth("kiro-expired.json")
	authstatus.MarkLoginRequired(auth.Metadata, "token refresh failed (status 400): invalid_grant", time.Now())
	m := newCooldownSyncManager(t, store, auth)

	store.mu.Lock()
	store.load = []CooldownStateRecord{{
		AuthID:         "kiro-expired.json",
		Model:          "claude-sonnet-4",
		Status:         "cooling",
		Reason:         "quota",
		NextRetryAfter: time.Now().Add(-time.Minute),
	}}
	store.mu.Unlock()
	if err := m.SyncCooldownStatesFromStore(context.Background()); err != nil {
		t.Fatal(err)
	}
	current, _ := m.GetByID("kiro-expired.json")
	if current.Unavailable || len(current.ModelStates) != 0 {
		t.Fatalf("expired record should not mark the auth unavailable: %+v", current)
	}
	if current.Status != StatusError || current.StatusMessage == "" || !hasUnauthorizedAuthFailure(current) {
		t.Fatalf("login_required marker should survive a sync: status=%s msg=%q", current.Status, current.StatusMessage)
	}
}
