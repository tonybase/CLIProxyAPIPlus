package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/authstatus"
)

// reloadedFrom mimics the service reloading an auth file: the new metadata comes from
// disk while the runtime failure state is carried over from the registered auth.
func reloadedFrom(existing *Auth, metadata map[string]any) *Auth {
	next := existing.Clone()
	next.Metadata = metadata
	return next
}

func newCredentialTestManager(t *testing.T, auth *Auth) *Manager {
	t.Helper()
	m := NewManager(nil, nil, nil)
	if _, err := m.Register(WithSkipPersist(context.Background()), auth); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestUpdate_ReLoginClearsLoginRequiredState(t *testing.T) {
	auth := fileAuth("kiro-relogin.json")
	authstatus.MarkLoginRequired(auth.Metadata, "token refresh failed (status 400): invalid_grant", time.Now())
	m := newCredentialTestManager(t, auth)
	existing, _ := m.GetByID(auth.ID)
	if existing.Status != StatusError || !hasUnauthorizedAuthFailure(existing) {
		t.Fatalf("marked auth should start unauthorized: %+v", existing)
	}

	relogin := reloadedFrom(existing, map[string]any{"type": "kiro", "access_token": "new", "refresh_token": "new-rt"})
	if _, err := m.Update(WithSkipPersist(context.Background()), relogin); err != nil {
		t.Fatal(err)
	}

	current, _ := m.GetByID(auth.ID)
	if current.Status != StatusActive || current.LastError != nil || current.StatusMessage != "" || current.Unavailable {
		t.Fatalf("re-login should restore the auth: status=%s msg=%q unavailable=%v err=%v",
			current.Status, current.StatusMessage, current.Unavailable, current.LastError)
	}
	if hasUnauthorizedAuthFailure(current) {
		t.Fatal("re-login should make the auth refreshable again")
	}
}

func TestUpdate_CredentialChangeClearsUnauthorizedButKeepsQuota(t *testing.T) {
	now := time.Now()
	auth := fileAuth("kiro-quota.json")
	auth.Status = StatusError
	auth.ModelStates = map[string]*ModelState{
		"model-401": {
			Status:         StatusError,
			Unavailable:    true,
			NextRetryAfter: now.Add(30 * time.Minute),
			LastError:      &Error{Code: "unauthorized", Message: "bearer token invalid", HTTPStatus: http.StatusUnauthorized},
		},
		"model-429": {
			Status:         StatusError,
			Unavailable:    true,
			NextRetryAfter: now.Add(time.Hour),
			LastError:      &Error{Message: "rate limited", HTTPStatus: http.StatusTooManyRequests},
			Quota:          QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: now.Add(time.Hour)},
		},
	}
	m := newCredentialTestManager(t, auth)
	existing, _ := m.GetByID(auth.ID)

	refreshed := reloadedFrom(existing, map[string]any{"type": "kiro", "access_token": "rotated", "refresh_token": "rt"})
	if _, err := m.Update(WithSkipPersist(context.Background()), refreshed); err != nil {
		t.Fatal(err)
	}

	current, _ := m.GetByID(auth.ID)
	if state := current.ModelStates["model-401"]; state.Unavailable || state.LastError != nil {
		t.Fatalf("401 model state should be cleared: %+v", state)
	}
	if state := current.ModelStates["model-429"]; !state.Unavailable || !state.Quota.Exceeded {
		t.Fatalf("quota cooldown should be kept: %+v", state)
	}
	if !current.Quota.Exceeded {
		t.Fatal("aggregated quota should still be exceeded")
	}
	if original := existing.ModelStates["model-401"]; !original.Unavailable {
		t.Fatal("clearing must not mutate the previous auth's model states")
	}
}

func TestUpdate_SameCredentialKeepsFailureState(t *testing.T) {
	now := time.Now()
	auth := fileAuth("kiro-same.json")
	auth.Status = StatusError
	auth.StatusMessage = "unauthorized"
	auth.Unavailable = true
	auth.NextRetryAfter = now.Add(30 * time.Minute)
	auth.LastError = &Error{Code: "unauthorized", Message: "bearer token invalid", HTTPStatus: http.StatusUnauthorized}
	m := newCredentialTestManager(t, auth)
	existing, _ := m.GetByID(auth.ID)

	// Same tokens, only an unrelated field changed (e.g. priority edited in the file).
	metadata := map[string]any{"type": "kiro", "access_token": "old", "refresh_token": "rt", "priority": 5}
	if _, err := m.Update(WithSkipPersist(context.Background()), reloadedFrom(existing, metadata)); err != nil {
		t.Fatal(err)
	}

	current, _ := m.GetByID(auth.ID)
	if current.Status != StatusError || current.LastError == nil || !current.Unavailable {
		t.Fatalf("unchanged credential should keep the failure state: %+v", current)
	}
}

func TestUpdate_FileStillMarkedStaysUnauthorized(t *testing.T) {
	auth := fileAuth("kiro-still-marked.json")
	authstatus.MarkLoginRequired(auth.Metadata, "invalid_grant", time.Now())
	m := newCredentialTestManager(t, auth)
	existing, _ := m.GetByID(auth.ID)

	// A rewrite that keeps the marker, e.g. a peer writing another field, is not a re-login.
	metadata := map[string]any{"type": "kiro", "access_token": "other", "refresh_token": "rt"}
	authstatus.MarkLoginRequired(metadata, "invalid_grant", time.Now())
	if _, err := m.Update(WithSkipPersist(context.Background()), reloadedFrom(existing, metadata)); err != nil {
		t.Fatal(err)
	}

	current, _ := m.GetByID(auth.ID)
	if current.Status != StatusError || !hasUnauthorizedAuthFailure(current) {
		t.Fatalf("auth whose file still carries the marker should stay unauthorized: %+v", current)
	}
}
