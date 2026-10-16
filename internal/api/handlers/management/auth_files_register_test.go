package management

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestRegisterAuthFile_RegistersWithoutWatcher(t *testing.T) {
	authDir := t.TempDir()
	fileName := "kiro-builder-id-user-example-com.json"
	path := filepath.Join(authDir, fileName)
	if errWrite := os.WriteFile(path, []byte(`{"type":"kiro","email":"user@example.com","access_token":"a","refresh_token":"r"}`), 0o600); errWrite != nil {
		t.Fatalf("failed to write auth file: %v", errWrite)
	}

	manager := coreauth.NewManager(nil, nil, nil)
	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)

	if errRegister := h.RegisterAuthFile(context.Background(), path); errRegister != nil {
		t.Fatalf("RegisterAuthFile returned error: %v", errRegister)
	}

	auths := manager.List()
	if len(auths) != 1 {
		t.Fatalf("registered auths = %d, want 1", len(auths))
	}
	if auths[0].FileName != fileName {
		t.Fatalf("file name = %q, want %q", auths[0].FileName, fileName)
	}
	if auths[0].Provider != "kiro" {
		t.Fatalf("provider = %q, want kiro", auths[0].Provider)
	}
}

func TestRegisterAuthFile_UsesPersistHookWhenSet(t *testing.T) {
	authDir := t.TempDir()
	path := filepath.Join(authDir, "kiro-idc-user-example-com.json")
	if errWrite := os.WriteFile(path, []byte(`{"type":"kiro","email":"user@example.com"}`), 0o600); errWrite != nil {
		t.Fatalf("failed to write auth file: %v", errWrite)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, coreauth.NewManager(nil, nil, nil))
	var hooked *coreauth.Auth
	h.SetPostAuthPersistHook(func(_ context.Context, auth *coreauth.Auth) error {
		hooked = auth
		return nil
	})

	if errRegister := h.RegisterAuthFile(context.Background(), path); errRegister != nil {
		t.Fatalf("RegisterAuthFile returned error: %v", errRegister)
	}
	if hooked == nil {
		t.Fatal("post-auth persist hook was not called")
	}
	if hooked.Attributes["path"] == "" {
		t.Fatal("hooked auth is missing path attribute")
	}
}
