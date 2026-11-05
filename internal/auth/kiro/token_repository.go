package kiro

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/authshard"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/authstatus"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	log "github.com/sirupsen/logrus"
)

// FileTokenRepository implements TokenRepository on top of the auth directory.
type FileTokenRepository struct {
	mu      sync.RWMutex
	baseDir string
}

// NewFileTokenRepository creates a file-backed token repository.
func NewFileTokenRepository(baseDir string) *FileTokenRepository {
	return &FileTokenRepository{
		baseDir: baseDir,
	}
}

// SetBaseDir sets the auth directory.
func (r *FileTokenRepository) SetBaseDir(dir string) {
	r.mu.Lock()
	r.baseDir = strings.TrimSpace(dir)
	r.mu.Unlock()
}

// FindOldestUnverified returns tokens that need a refresh, least recently verified first.
func (r *FileTokenRepository) FindOldestUnverified(limit int) []*Token {
	r.mu.RLock()
	baseDir := r.baseDir
	r.mu.RUnlock()

	if baseDir == "" {
		log.Debug("token repository: base directory not configured")
		return nil
	}

	var tokens []*Token

	err := filepath.WalkDir(baseDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // Ignore errors and keep walking
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".json") {
			return nil
		}

		// Only refresh kiro token files this node is responsible for.
		if !strings.HasPrefix(d.Name(), "kiro-") || !authshard.Refreshes(d.Name()) {
			return nil
		}

		token, err := r.readTokenFile(path)
		if err != nil {
			log.Debugf("token repository: failed to read token file %s: %v", path, err)
			return nil
		}

		// A rejected refresh token only recovers through a new login, so stop retrying it.
		if token != nil && token.RefreshToken != "" && !token.LoginRequired {
			// Refresh tokens that expire within 5 minutes.
			if token.ExpiresAt.IsZero() || time.Until(token.ExpiresAt) < 5*time.Minute {
				tokens = append(tokens, token)
			}
		}

		return nil
	})

	if err != nil {
		log.Warnf("token repository: error walking directory: %v", err)
	}

	// Oldest verification first.
	sort.Slice(tokens, func(i, j int) bool {
		return tokens[i].LastVerified.Before(tokens[j].LastVerified)
	})

	if limit > 0 && len(tokens) > limit {
		tokens = tokens[:limit]
	}

	return tokens
}

// UpdateToken persists a refreshed token to its auth file.
func (r *FileTokenRepository) UpdateToken(token *Token) error {
	if token == nil {
		return fmt.Errorf("token repository: token is nil")
	}

	filePath, existingData, err := r.loadTokenFile(token.ID)
	if err != nil {
		return err
	}

	existingData["access_token"] = token.AccessToken
	existingData["refresh_token"] = token.RefreshToken
	existingData["last_refresh"] = time.Now().Format(time.RFC3339)

	if !token.ExpiresAt.IsZero() {
		existingData["expires_at"] = token.ExpiresAt.Format(time.RFC3339)
	}

	// Keep the fields required for future refreshes.
	if token.ClientID != "" {
		existingData["client_id"] = token.ClientID
	}
	if token.ClientSecret != "" {
		existingData["client_secret"] = token.ClientSecret
	}
	if token.AuthMethod != "" {
		existingData["auth_method"] = token.AuthMethod
	}
	if token.Region != "" {
		existingData["region"] = token.Region
	}
	if token.StartURL != "" {
		existingData["start_url"] = token.StartURL
	}

	// A successful refresh proves the account is still signed in.
	authstatus.ClearLoginRequired(existingData)

	if err := writeTokenFile(filePath, existingData); err != nil {
		return err
	}
	log.Debugf("token repository: updated token %s", token.ID)
	return nil
}

// MarkLoginRequired records in the token's auth file that its refresh credential was
// rejected, so every node sharing the auth directory can see the account needs a new login.
func (r *FileTokenRepository) MarkLoginRequired(token *Token, reason string) error {
	if token == nil {
		return fmt.Errorf("token repository: token is nil")
	}
	filePath, existingData, err := r.loadTokenFile(token.ID)
	if err != nil {
		return err
	}
	if !authstatus.MarkLoginRequired(existingData, reason, time.Now()) {
		return nil
	}
	if err := writeTokenFile(filePath, existingData); err != nil {
		return err
	}
	token.LoginRequired = true
	log.Warnf("token repository: token %s needs a new login: %s", token.ID, reason)
	return nil
}

// loadTokenFile resolves the auth file for id and returns its current contents.
func (r *FileTokenRepository) loadTokenFile(id string) (string, map[string]any, error) {
	r.mu.RLock()
	baseDir := r.baseDir
	r.mu.RUnlock()

	if baseDir == "" {
		return "", nil, fmt.Errorf("token repository: base directory not configured")
	}

	filePath := filepath.Join(baseDir, id)
	if !strings.HasSuffix(filePath, ".json") {
		filePath += ".json"
	}

	existingData := make(map[string]any)
	if data, err := os.ReadFile(filePath); err == nil {
		_ = json.Unmarshal(data, &existingData)
	}
	return filePath, existingData, nil
}

// writeTokenFile atomically replaces the auth file, so peers on a shared directory never read a torn file.
func writeTokenFile(filePath string, data map[string]any) error {
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("token repository: marshal failed: %w", err)
	}
	if err := util.WriteJSONFileAtomic(filePath, raw, 0o600); err != nil {
		return fmt.Errorf("token repository: write failed: %w", err)
	}
	return nil
}

// readTokenFile reads a token from an auth file.
func (r *FileTokenRepository) readTokenFile(path string) (*Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var metadata map[string]any
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, err
	}

	// Only kiro tokens.
	tokenType, _ := metadata["type"].(string)
	if tokenType != "kiro" {
		return nil, nil
	}

	// Check auth_method (case-insensitive comparison to handle "IdC", "IDC", "idc", etc.)
	authMethod, _ := metadata["auth_method"].(string)
	authMethod = strings.ToLower(authMethod)
	if authMethod != "idc" && authMethod != "builder-id" {
		return nil, nil // Only IDC and Builder ID tokens are refreshed here
	}

	token := &Token{
		ID:         filepath.Base(path),
		AuthMethod: authMethod,
	}

	token.AccessToken, _ = metadata["access_token"].(string)
	token.RefreshToken, _ = metadata["refresh_token"].(string)
	token.ClientID, _ = metadata["client_id"].(string)
	token.ClientSecret, _ = metadata["client_secret"].(string)
	token.Region, _ = metadata["region"].(string)
	token.StartURL, _ = metadata["start_url"].(string)
	token.Provider, _ = metadata["provider"].(string)
	token.LoginRequired, _, _ = authstatus.LoginRequired(metadata)

	if expiresAtStr, ok := metadata["expires_at"].(string); ok && expiresAtStr != "" {
		if t, err := time.Parse(time.RFC3339, expiresAtStr); err == nil {
			token.ExpiresAt = t
		}
	}
	if lastRefreshStr, ok := metadata["last_refresh"].(string); ok && lastRefreshStr != "" {
		if t, err := time.Parse(time.RFC3339, lastRefreshStr); err == nil {
			token.LastVerified = t
		}
	}

	return token, nil
}

// ListKiroTokens lists the Kiro tokens loaded by this node (for debugging).
func (r *FileTokenRepository) ListKiroTokens(ctx context.Context) ([]*Token, error) {
	r.mu.RLock()
	baseDir := r.baseDir
	r.mu.RUnlock()

	if baseDir == "" {
		return nil, fmt.Errorf("token repository: base directory not configured")
	}

	var tokens []*Token

	err := filepath.WalkDir(baseDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasPrefix(d.Name(), "kiro-") || !strings.HasSuffix(d.Name(), ".json") || !authshard.Owns(d.Name()) {
			return nil
		}

		token, err := r.readTokenFile(path)
		if err != nil {
			return nil
		}
		if token != nil {
			tokens = append(tokens, token)
		}
		return nil
	})

	return tokens, err
}
