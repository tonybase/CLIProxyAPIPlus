package auth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/authshard"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/authstatus"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
)

type recordingAuthStore struct {
	mu    sync.Mutex
	saved []*Auth
	list  []*Auth
}

func (s *recordingAuthStore) List(context.Context) ([]*Auth, error) { return s.list, nil }

func (s *recordingAuthStore) Save(_ context.Context, auth *Auth) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saved = append(s.saved, auth.Clone())
	return "", nil
}

func (s *recordingAuthStore) Delete(context.Context, string) error { return nil }

func (s *recordingAuthStore) lastSaved() *Auth {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.saved) == 0 {
		return nil
	}
	return s.saved[len(s.saved)-1]
}

func resetAuthShard(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { _, _ = authshard.Configure(config.AuthShardConfig{}) })
}

func fileAuth(id string) *Auth {
	return &Auth{
		ID:       id,
		Provider: "kiro-test",
		Attributes: map[string]string{
			AttributePath:          "/auths/" + id,
			AttributeSourceBackend: AuthSourceFile,
		},
		Metadata: map[string]any{"type": "kiro", "access_token": "old", "refresh_token": "rt"},
	}
}

func TestRefreshFailure_PersistsLoginRequiredMarker(t *testing.T) {
	store := &recordingAuthStore{}
	executor := &unauthorizedRefreshExecutor{id: "kiro-test", refreshFail: true}
	m := NewManager(store, nil, nil)
	m.RegisterExecutor(executor)
	if _, err := m.Register(WithSkipPersist(context.Background()), fileAuth("kiro-a.json")); err != nil {
		t.Fatal(err)
	}

	m.refreshAuth(context.Background(), "kiro-a.json")

	saved := store.lastSaved()
	if saved == nil {
		t.Fatal("refresh failure did not persist the auth")
	}
	required, reason, at := authstatus.LoginRequired(saved.Metadata)
	if !required || reason == "" || at == "" {
		t.Fatalf("persisted metadata = %#v, want login_required marker", saved.Metadata)
	}
	current, _ := m.GetByID("kiro-a.json")
	if current == nil || !hasUnauthorizedAuthFailure(current) {
		t.Fatalf("auth should stay unauthorized after a rejected refresh: %#v", current)
	}

	// A second identical failure must not rewrite the file.
	saves := len(store.saved)
	if _, err := m.refreshAuthForRequest(context.Background(), "kiro-a.json", ""); err == nil {
		t.Fatal("expected refresh error")
	}
	if len(store.saved) != saves {
		t.Fatalf("unchanged marker rewrote the auth file (%d -> %d saves)", saves, len(store.saved))
	}
}

func TestRefreshSuccess_ClearsLoginRequiredMarker(t *testing.T) {
	store := &recordingAuthStore{}
	executor := &unauthorizedRefreshExecutor{id: "kiro-test"}
	m := NewManager(store, nil, nil)
	m.RegisterExecutor(executor)
	auth := fileAuth("kiro-b.json")
	authstatus.MarkLoginRequired(auth.Metadata, "token refresh failed (status 400): invalid_grant", time.Now())
	if _, err := m.Register(WithSkipPersist(context.Background()), auth); err != nil {
		t.Fatal(err)
	}
	if current, _ := m.GetByID("kiro-b.json"); !hasUnauthorizedAuthFailure(current) {
		t.Fatal("marker in metadata should mark the auth unauthorized on register")
	}

	if _, err := m.refreshAuthForRequest(context.Background(), "kiro-b.json", ""); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	saved := store.lastSaved()
	if saved == nil {
		t.Fatal("successful refresh did not persist")
	}
	if required, _, _ := authstatus.LoginRequired(saved.Metadata); required {
		t.Fatalf("marker not cleared after successful refresh: %#v", saved.Metadata)
	}
}

func TestManagerNode_DoesNotRefreshStoredAuths(t *testing.T) {
	resetAuthShard(t)
	if _, err := authshard.Configure(config.AuthShardConfig{Enabled: true, Role: authshard.RoleManager}); err != nil {
		t.Fatal(err)
	}
	executor := &unauthorizedRefreshExecutor{id: "kiro-test"}
	m := NewManager(nil, nil, nil)
	m.RegisterExecutor(executor)
	auth := fileAuth("kiro-c.json")
	if _, err := m.Register(context.Background(), auth); err != nil {
		t.Fatal(err)
	}
	current, _ := m.GetByID("kiro-c.json")
	if m.shouldRefresh(current, time.Now()) {
		t.Fatal("manager node should not schedule refreshes for stored auths")
	}
	if _, err := m.refreshAuthForRequest(context.Background(), "kiro-c.json", ""); err == nil {
		t.Fatal("manager node refresh should be refused")
	}
	if executor.refreshCalls != 0 {
		t.Fatalf("executor Refresh called %d times on manager node", executor.refreshCalls)
	}
}

func TestLoad_KeepsOnlyOwnedStoredAuths(t *testing.T) {
	resetAuthShard(t)
	var list []*Auth
	for i := range 10 {
		list = append(list, fileAuth(fmt.Sprintf("kiro-user%d.json", i)))
	}
	seen := make(map[string]int)
	for index := range 2 {
		if _, err := authshard.Configure(config.AuthShardConfig{Enabled: true, NodeCount: 2, NodeIndex: index}); err != nil {
			t.Fatal(err)
		}
		m := NewManager(&recordingAuthStore{list: list}, nil, nil)
		if err := m.Load(context.Background()); err != nil {
			t.Fatal(err)
		}
		for _, auth := range m.List() {
			seen[auth.ID]++
			if authshard.OwnerOf(auth.ID, 2) != index {
				t.Fatalf("node %d loaded %s owned by another node", index, auth.ID)
			}
		}
	}
	if len(seen) != len(list) {
		t.Fatalf("shards cover %d of %d auths", len(seen), len(list))
	}
}

func TestCooldownStateSave_KeepsPeerStateFiles(t *testing.T) {
	resetAuthShard(t)
	if _, err := authshard.Configure(config.AuthShardConfig{Enabled: true, NodeCount: 2, NodeIndex: 0}); err != nil {
		t.Fatal(err)
	}
	var owned, peer string
	for i := 0; owned == "" || peer == ""; i++ {
		name := fmt.Sprintf("kiro-user%d.json", i)
		if authshard.OwnerOf(name, 2) == 0 {
			owned = name
		} else {
			peer = name
		}
	}

	dir := t.TempDir()
	store := NewFileCooldownStateStoreWithAuthDir(dir, dir)
	until := time.Now().Add(time.Hour)
	peerRecord := CooldownStateRecord{AuthID: peer, Status: "cooling", NextRetryAfter: until}
	peerPath, errPath := store.statePath(peerRecord)
	if errPath != nil {
		t.Fatal(errPath)
	}
	if err := writeCooldownStateGroup(context.Background(), peerPath, []CooldownStateRecord{peerRecord}); err != nil {
		t.Fatal(err)
	}

	ownedRecord := CooldownStateRecord{AuthID: owned, Status: "cooling", NextRetryAfter: until}
	if err := store.Save(context.Background(), []CooldownStateRecord{ownedRecord, peerRecord}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(peerPath); err != nil {
		t.Fatalf("peer cooldown state was removed: %v", err)
	}
	ownedPath, _ := store.statePath(ownedRecord)
	if _, err := os.Stat(ownedPath); !os.IsNotExist(err) {
		t.Fatalf("owned cooldown state should be cleaned up, stat err = %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(peerPath))
	if len(entries) != 1 {
		t.Fatalf("state dir has %d files, want only the peer file", len(entries))
	}
}
