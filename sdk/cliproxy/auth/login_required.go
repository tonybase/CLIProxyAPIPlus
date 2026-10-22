package auth

import (
	"net/http"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/authstatus"
)

// IsLoginRequiredError reports whether a refresh error means the stored refresh
// credential was rejected, so only a new login can recover the account.
func IsLoginRequiredError(err error) bool {
	if err == nil {
		return false
	}
	return isUnauthorizedError(err) || authstatus.IsLoginRequiredMessage(err.Error())
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
