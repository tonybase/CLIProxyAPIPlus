package kiro

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/authstatus"
)

func writeKiroTokenFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{
		"type":          "kiro",
		"auth_method":   "idc",
		"access_token":  "access",
		"refresh_token": "refresh",
		"client_id":     "client",
		"client_secret": "secret",
		"region":        "us-east-1",
		"expires_at":    time.Now().Add(-time.Minute).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readKiroTokenFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFileTokenRepository_LoginRequiredLifecycle(t *testing.T) {
	dir := t.TempDir()
	path := writeKiroTokenFile(t, dir, "kiro-idc-a.json")
	repo := NewFileTokenRepository(dir)

	tokens := repo.FindOldestUnverified(10)
	if len(tokens) != 1 {
		t.Fatalf("expected 1 expiring token, got %d", len(tokens))
	}

	if err := repo.MarkLoginRequired(tokens[0], "token refresh failed (status 400): invalid_grant"); err != nil {
		t.Fatal(err)
	}
	if required, reason, _ := authstatus.LoginRequired(readKiroTokenFile(t, path)); !required || reason == "" {
		t.Fatal("marker not written to the auth file")
	}
	if got := repo.FindOldestUnverified(10); len(got) != 0 {
		t.Fatalf("token marked login_required should not be refreshed again, got %d", len(got))
	}

	tokens[0].AccessToken = "new-access"
	tokens[0].ExpiresAt = time.Now().Add(time.Hour)
	if err := repo.UpdateToken(tokens[0]); err != nil {
		t.Fatal(err)
	}
	updated := readKiroTokenFile(t, path)
	if required, _, _ := authstatus.LoginRequired(updated); required {
		t.Fatal("successful refresh should clear the marker")
	}
	if updated["access_token"] != "new-access" || updated["client_secret"] != "secret" {
		t.Fatalf("update lost token fields: %#v", updated)
	}
}
