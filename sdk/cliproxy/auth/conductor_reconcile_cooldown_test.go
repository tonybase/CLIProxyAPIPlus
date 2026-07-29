package auth

import (
	"context"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
)

// TestReconcileRegistryModelStates_PreservesActiveCooldown asserts that a model
// cooldown which has not elapsed survives reconciliation. Credential refresh
// rewrites the auth file on a timer, and the resulting reload must not resurrect
// a credential the upstream still rejects.
func TestReconcileRegistryModelStates_PreservesActiveCooldown(t *testing.T) {
	const (
		provider    = "kiro"
		cooledModel = "kiro-claude-sonnet-4-6"
		staleModel  = "kiro-claude-opus-4-7"
	)

	manager := NewManager(nil, nil, nil)
	nextRetry := time.Now().Add(24 * time.Hour)

	auth := &Auth{
		ID:       "kiro-cooldown-auth",
		Provider: provider,
		Status:   StatusError,
		ModelStates: map[string]*ModelState{
			cooledModel: {
				Unavailable:    true,
				Status:         StatusError,
				StatusMessage:  "payment_required",
				NextRetryAfter: nextRetry,
				Quota: QuotaState{
					Exceeded:      true,
					Reason:        "payment_required",
					NextRecoverAt: nextRetry,
				},
			},
			// An elapsed cooldown must still be reset so recovery works.
			staleModel: {
				Unavailable:    true,
				Status:         StatusError,
				NextRetryAfter: time.Now().Add(-1 * time.Hour),
			},
		},
	}
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("register auth: %v", errRegister)
	}

	reg := registry.GetGlobalRegistry()
	reg.RegisterClient(auth.ID, provider, []*registry.ModelInfo{{ID: cooledModel}, {ID: staleModel}})
	t.Cleanup(func() {
		reg.UnregisterClient(auth.ID)
	})

	manager.ReconcileRegistryModelStates(context.Background(), auth.ID)

	stored, ok := manager.GetByID(auth.ID)
	if !ok || stored == nil {
		t.Fatalf("auth %s missing after reconciliation", auth.ID)
	}

	cooled := stored.ModelStates[cooledModel]
	if cooled == nil {
		t.Fatalf("model state for %s was dropped", cooledModel)
	}
	if !cooled.Unavailable {
		t.Errorf("cooled model Unavailable = false, want true")
	}
	if !cooled.NextRetryAfter.Equal(nextRetry) {
		t.Errorf("cooled model NextRetryAfter = %v, want %v", cooled.NextRetryAfter, nextRetry)
	}
	if !cooled.Quota.Exceeded {
		t.Errorf("cooled model Quota.Exceeded = false, want true")
	}

	stale := stored.ModelStates[staleModel]
	if stale == nil {
		t.Fatalf("model state for %s was dropped", staleModel)
	}
	if stale.Unavailable {
		t.Errorf("elapsed cooldown Unavailable = true, want false")
	}
	if !stale.NextRetryAfter.IsZero() {
		t.Errorf("elapsed cooldown NextRetryAfter = %v, want zero", stale.NextRetryAfter)
	}

	// The selector must still refuse the cooled model and accept the recovered one.
	if blocked, reason, _ := isAuthBlockedForModel(stored, cooledModel, time.Now()); !blocked || reason != blockReasonCooldown {
		t.Errorf("isAuthBlockedForModel(%s) = (%v, %v), want (true, blockReasonCooldown)", cooledModel, blocked, reason)
	}
	if blocked, _, _ := isAuthBlockedForModel(stored, staleModel, time.Now()); blocked {
		t.Errorf("isAuthBlockedForModel(%s) = true, want false", staleModel)
	}
}
