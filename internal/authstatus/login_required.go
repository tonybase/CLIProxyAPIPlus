// Package authstatus defines account state markers persisted inside auth files, so
// every node sharing one auth directory (including a manager node that never
// refreshes credentials) can see them.
package authstatus

import (
	"strings"
	"time"
)

// Metadata keys written into an auth file when its refresh credential is rejected and
// the account has to be signed in again. A new login rewrites the file without them.
const (
	KeyLoginRequired       = "login_required"
	KeyLoginRequiredReason = "login_required_reason"
	KeyLoginRequiredAt     = "login_required_at"
)

const maxReasonRunes = 300

// IsLoginRequiredMessage reports whether a refresh error message means the stored
// refresh credential was rejected, so only a new login can recover the account.
func IsLoginRequiredMessage(msg string) bool {
	raw := strings.ToLower(msg)
	// OAuth/OIDC token endpoints answer a revoked refresh token or an expired client
	// registration with HTTP 400 and invalid_grant / invalid_client / expired_token.
	return strings.Contains(raw, "status 401") ||
		strings.Contains(raw, "401 unauthorized") ||
		strings.Contains(raw, "invalid_grant") ||
		strings.Contains(raw, "invalid_client") ||
		strings.Contains(raw, "expired_token")
}

// IsKiroLoginRequiredMessage extends IsLoginRequiredMessage with the Kiro-specific
// rejection: AWS SSO OIDC (Kiro IDC) answers a refresh token that is no longer usable
// with HTTP 400 invalid_request, while the same request succeeds for healthy accounts.
// It is kept separate because other providers use invalid_request for malformed input.
func IsKiroLoginRequiredMessage(msg string) bool {
	if IsLoginRequiredMessage(msg) {
		return true
	}
	raw := strings.ToLower(msg)
	return strings.Contains(raw, "status 400") && strings.Contains(raw, "invalid_request")
}

// MarkLoginRequired records the login-required marker in metadata. It reports whether
// metadata changed, so callers rewrite the auth file only when the state changes.
func MarkLoginRequired(metadata map[string]any, reason string, now time.Time) bool {
	if metadata == nil {
		return false
	}
	reason = strings.TrimSpace(reason)
	if runes := []rune(reason); len(runes) > maxReasonRunes {
		reason = string(runes[:maxReasonRunes])
	}
	if required, prevReason, _ := LoginRequired(metadata); required && prevReason == reason {
		return false
	}
	metadata[KeyLoginRequired] = true
	metadata[KeyLoginRequiredReason] = reason
	metadata[KeyLoginRequiredAt] = now.UTC().Format(time.RFC3339)
	return true
}

// ClearLoginRequired removes the login-required marker and reports whether one was present.
func ClearLoginRequired(metadata map[string]any) bool {
	if metadata == nil {
		return false
	}
	changed := false
	for _, key := range []string{KeyLoginRequired, KeyLoginRequiredReason, KeyLoginRequiredAt} {
		if _, ok := metadata[key]; ok {
			delete(metadata, key)
			changed = true
		}
	}
	return changed
}

// LoginRequired returns the login-required marker stored in metadata.
func LoginRequired(metadata map[string]any) (required bool, reason, at string) {
	if metadata == nil {
		return false, "", ""
	}
	required, _ = metadata[KeyLoginRequired].(bool)
	if !required {
		return false, "", ""
	}
	reason, _ = metadata[KeyLoginRequiredReason].(string)
	at, _ = metadata[KeyLoginRequiredAt].(string)
	return true, reason, at
}
