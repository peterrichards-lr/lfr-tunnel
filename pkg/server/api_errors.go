package server

import (
	"errors"
	"net/http"
)

// Sentinel API errors for service layer returns
var (
	ErrUnauthorized       = errors.New("unauthorized")
	ErrForbidden          = errors.New("forbidden")
	ErrInvalidRequest     = errors.New("invalid request")
	ErrNotFound           = errors.New("not found")
	ErrConflict           = errors.New("conflict")
	ErrQuotaReached       = errors.New("quota limit reached")
	ErrInternalError      = errors.New("server error")
	ErrTokenExpired       = errors.New("token expired or invalid")
	ErrDomainNotSupported = errors.New("domain is not supported by this gateway")
	ErrQuarantined        = errors.New("subdomain is currently quarantined by another user")
)

// mapErrorToStatusCode converts our service errors into HTTP status codes.
func mapErrorToStatusCode(err error) int {
	if errors.Is(err, ErrUnauthorized) {
		return http.StatusUnauthorized
	}
	if errors.Is(err, ErrForbidden) {
		return http.StatusForbidden
	}
	if errors.Is(err, ErrInvalidRequest) || errors.Is(err, ErrDomainNotSupported) || errors.Is(err, ErrQuotaReached) {
		return http.StatusBadRequest
	}
	if errors.Is(err, ErrNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrQuarantined) {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

// respondWithError gracefully converts an error to a JSON response
func respondWithError(w http.ResponseWriter, err error) {
	status := mapErrorToStatusCode(err)
	// Some errors have custom messages we want to expose directly, otherwise generic
	msg := err.Error()
	if status == http.StatusInternalServerError {
		msg = "Server error"
	}
	http.Error(w, `{"error":"`+msg+`"}`, status)
}
