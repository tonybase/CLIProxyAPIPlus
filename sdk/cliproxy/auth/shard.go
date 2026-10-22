package auth

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/authshard"
)

// authShardName returns the name used to decide auth-shard ownership: the backing
// file path when known, otherwise the auth ID (stored auth IDs are file names).
func authShardName(auth *Auth) string {
	if auth == nil {
		return ""
	}
	if path := strings.TrimSpace(auth.Attributes[AttributePath]); path != "" {
		return path
	}
	return auth.ID
}

// isStoredAuth reports whether auth comes from the shared auth store (file, git,
// object store or Postgres) rather than config entries or runtime-only registration.
func isStoredAuth(auth *Auth) bool {
	if auth == nil {
		return false
	}
	switch auth.AuthSourceKind() {
	case AuthSourceFile, AuthSourceGit, AuthSourceObjectStore, AuthSourcePostgres:
		return true
	default:
		return false
	}
}

// loadedByThisNode reports whether a stored auth belongs on this node in a sharded auth directory.
func loadedByThisNode(auth *Auth) bool {
	return auth != nil && authshard.Owns(authShardName(auth))
}

// cooldownRecordOwned reports whether this node writes the cooldown state for record.
func cooldownRecordOwned(record CooldownStateRecord) bool {
	if !authshard.Enabled() {
		return true
	}
	name := strings.TrimSpace(record.AuthFile)
	if name == "" {
		name = record.AuthID
	}
	return authshard.Refreshes(name)
}

// cooldownStateFileOwned reports whether this node may delete the .cds file at path.
// The file name is sanitized and cannot be mapped back to the auth file, so ownership
// is read from the auth ID stored inside it. Unreadable files are left in place.
func cooldownStateFileOwned(path string) bool {
	if !authshard.Enabled() {
		return true
	}
	data, errRead := os.ReadFile(path)
	if errRead != nil {
		return false
	}
	var envelope cooldownStateFile
	if errUnmarshal := json.Unmarshal(data, &envelope); errUnmarshal != nil {
		return false
	}
	authID := strings.TrimSpace(envelope.AuthID)
	if authID == "" {
		return false
	}
	return authshard.Refreshes(authID)
}

// RefreshesOnThisNode reports whether this node may refresh auth. In a sharded auth
// directory a stored auth is refreshed only by its owning worker, and a manager node
// refreshes none of them. Config and runtime-only auths are always refreshed locally.
func RefreshesOnThisNode(auth *Auth) bool {
	if !isStoredAuth(auth) {
		return true
	}
	return authshard.Refreshes(authShardName(auth))
}
