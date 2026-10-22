package auth

import (
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/authstatus"
)

// IsLoginRequiredError reports whether a refresh error from provider means the stored
// refresh credential was rejected, so only a new login can recover the account.
func IsLoginRequiredError(provider string, err error) bool {
	if err == nil {
		return false
	}
	if isUnauthorizedError(err) {
		return true
	}
	if strings.EqualFold(strings.TrimSpace(provider), "kiro") {
		return authstatus.IsKiroLoginRequiredMessage(err.Error())
	}
	return authstatus.IsLoginRequiredMessage(err.Error())
}

// ApplyLoginRequiredFromMetadata marks auth as unauthorized when its metadata carries a
// login-required marker. That keeps the auto-refresh loop from retrying a rejected
// credential until a new login replaces the file.
func ApplyLoginRequiredFromMetadata(auth *Auth) {
	if auth == nil {
		return
	}
	required, reason, _ := authstatus.LoginRequired(auth.Metadata)
	if !required {
		return
	}
	if reason == "" {
		reason = "login required"
	}
	if !auth.Disabled && auth.Status != StatusDisabled {
		auth.Status = StatusError
	}
	auth.StatusMessage = reason
	auth.LastError = &Error{Code: "unauthorized", Message: reason, HTTPStatus: http.StatusUnauthorized}
}

// credentialMetadataKeys are the metadata fields whose change means the stored
// credential was replaced by a new login or a successful refresh.
var credentialMetadataKeys = []string{"access_token", "accessToken", "refresh_token", "refreshToken"}

// credentialReplaced reports whether next carries a different credential than prev.
// It is derived from the auth content itself, so every writer (login, upload, refresh,
// a peer node on a shared auth-dir) is covered without bumping a version field.
func credentialReplaced(prev, next *Auth) bool {
	if prev == nil || next == nil {
		return false
	}
	// A file that still carries the marker has not been signed in again.
	if required, _, _ := authstatus.LoginRequired(next.Metadata); required {
		return false
	}
	if required, _, _ := authstatus.LoginRequired(prev.Metadata); required {
		return true
	}
	for _, key := range credentialMetadataKeys {
		if authMetadataString(prev, key) != authMetadataString(next, key) {
			return true
		}
	}
	return authAttribute(prev, AttributeAPIKey) != authAttribute(next, AttributeAPIKey)
}

// isCredentialFailure reports whether err was caused by a rejected credential.
func isCredentialFailure(err *Error) bool {
	if err == nil {
		return false
	}
	return err.StatusCode() == http.StatusUnauthorized ||
		strings.EqualFold(err.Code, "unauthorized") ||
		isInvalidGrantResultError(err)
}

// clearCredentialFailureState drops the failure state caused by a rejected credential
// once that credential has been replaced. Quota and other cooldowns are kept, since a
// new login does not reset them. It returns the models to resume in the registry and
// whether anything changed.
func clearCredentialFailureState(auth *Auth, now time.Time) ([]string, bool) {
	if auth == nil {
		return nil, false
	}
	var resumed []string
	if len(auth.ModelStates) > 0 {
		// The map may be shared with the previous auth, so never mutate its states in place.
		states := make(map[string]*ModelState, len(auth.ModelStates))
		for model, state := range auth.ModelStates {
			if state != nil && isCredentialFailure(state.LastError) {
				state = state.Clone()
				resetModelState(state, now)
				resumed = append(resumed, model)
			}
			states[model] = state
		}
		auth.ModelStates = states
	}
	authFailure := isCredentialFailure(auth.LastError)
	if !authFailure && len(resumed) == 0 {
		return nil, false
	}
	if authFailure {
		auth.LastError = nil
		auth.StatusMessage = ""
	}
	if len(auth.ModelStates) > 0 {
		updateAggregatedAvailability(auth, now)
	} else if !auth.Quota.Exceeded {
		auth.Unavailable = false
		auth.NextRetryAfter = time.Time{}
	}
	if auth.Status == StatusError && auth.LastError == nil && !hasModelError(auth, now) {
		auth.Status = StatusActive
		auth.StatusMessage = ""
	}
	auth.UpdatedAt = now
	return resumed, true
}
