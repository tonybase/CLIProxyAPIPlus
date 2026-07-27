package cliproxy

import (
	"context"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// coolingAuth mirrors an auth that hit a Kiro monthly limit (HTTP 402): the
// failure state lives only in memory.
func coolingAuth(id string, next time.Time) *coreauth.Auth {
	return &coreauth.Auth{
		ID:            id,
		FileName:      id + ".json",
		Provider:      "kiro",
		Status:        coreauth.StatusError,
		StatusMessage: `{"message":"You have reached the limit.","reason":"MONTHLY_REQUEST_COUNT"}`,
		Unavailable:   true,
		LastError: &coreauth.Error{
			Message:    "monthly limit reached",
			HTTPStatus: 402,
		},
		NextRetryAfter: next,
		Quota: coreauth.QuotaState{
			Exceeded:      true,
			Reason:        "quota_exhausted",
			NextRecoverAt: next,
			BackoffLevel:  2,
		},
	}
}

// reloadedFromDisk mirrors what the synthesizer builds after the auth file is
// rewritten: runtime-only fields are absent and Status is unconditionally active.
func reloadedFromDisk(id string) *coreauth.Auth {
	return &coreauth.Auth{
		ID:       id,
		FileName: id + ".json",
		Provider: "kiro",
		Status:   coreauth.StatusActive,
		Metadata: map[string]any{"access_token": "refreshed"},
	}
}

func TestPrepareCoreAuth_ReloadKeepsRuntimeFailureState(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	service := &Service{
		cfg:         &config.Config{AuthDir: t.TempDir()},
		coreManager: manager,
	}
	next := time.Now().Add(24 * time.Hour)

	if _, err := manager.Register(context.Background(), coolingAuth("auth-kiro-1", next)); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	// The Kiro background refresher rewrites the credential file every minute and
	// the watcher reloads it within seconds.
	service.prepareCoreAuthForModelRegistration(context.Background(), reloadedFromDisk("auth-kiro-1"))

	got, ok := manager.GetByID("auth-kiro-1")
	if !ok || got == nil {
		t.Fatalf("expected auth to be present")
	}
	if got.StatusMessage == "" {
		t.Errorf("expected StatusMessage to survive the reload, got empty")
	}
	if !got.Unavailable {
		t.Errorf("expected Unavailable to survive the reload")
	}
	if got.LastError == nil || got.LastError.HTTPStatus != 402 {
		t.Errorf("expected LastError to survive the reload, got %+v", got.LastError)
	}
	if !got.NextRetryAfter.Equal(next) {
		t.Errorf("expected NextRetryAfter %v, got %v", next, got.NextRetryAfter)
	}
	if !got.Quota.Exceeded || got.Quota.Reason != "quota_exhausted" || got.Quota.BackoffLevel != 2 {
		t.Errorf("expected Quota to survive the reload, got %+v", got.Quota)
	}
	// The reload reports StatusActive; keeping the error status is what makes the
	// management UI agree with the inherited cooldown fields.
	if got.Status != coreauth.StatusError {
		t.Errorf("expected StatusError to be retained, got %q", got.Status)
	}
}

// A successful token refresh explicitly clears the failure state and then calls
// Manager.Update directly. That clear must stand: inheriting state there would
// pin an auth in error after a 401 refresh recovered it.
func TestManagerUpdate_RefreshedAuthStaysCleared(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	next := time.Now().Add(24 * time.Hour)

	if _, err := manager.Register(context.Background(), coolingAuth("auth-kiro-2", next)); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	// Mirrors refreshAuthForRequest: clear the failure state, then Update.
	refreshed := reloadedFromDisk("auth-kiro-2")
	refreshed.LastError = nil
	refreshed.StatusMessage = ""
	refreshed.Unavailable = false
	if _, err := manager.Update(context.Background(), refreshed); err != nil {
		t.Fatalf("update auth: %v", err)
	}

	got, _ := manager.GetByID("auth-kiro-2")
	if got.StatusMessage != "" || got.Unavailable || got.LastError != nil {
		t.Errorf("expected a successful refresh to stay cleared, got %+v", got)
	}
	if got.Status != coreauth.StatusActive {
		t.Errorf("expected StatusActive after refresh, got %q", got.Status)
	}
}

func TestPrepareCoreAuth_DisabledReloadDoesNotInheritState(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	service := &Service{
		cfg:         &config.Config{AuthDir: t.TempDir()},
		coreManager: manager,
	}
	next := time.Now().Add(24 * time.Hour)

	if _, err := manager.Register(context.Background(), coolingAuth("auth-kiro-3", next)); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	incoming := reloadedFromDisk("auth-kiro-3")
	incoming.Disabled = true
	incoming.Status = coreauth.StatusDisabled
	service.prepareCoreAuthForModelRegistration(context.Background(), incoming)

	got, ok := manager.GetByID("auth-kiro-3")
	if !ok || got == nil {
		t.Fatalf("expected auth to be present")
	}
	if got.Unavailable || !got.NextRetryAfter.IsZero() || got.Quota.Exceeded {
		t.Errorf("expected disabling to clear cooldown state, got %+v", got)
	}
	if got.Status != coreauth.StatusDisabled {
		t.Errorf("expected StatusDisabled, got %q", got.Status)
	}
}
