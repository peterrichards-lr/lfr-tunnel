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
	// ErrPermanenceNotAllowed is returned when something asks for an expiry of never and the
	// operator's never_expires policy for that resource does not allow it (#2264). 403 rather
	// than 400: the request is well formed and would have worked on a gateway configured
	// differently, which is what "forbidden" means and "bad request" does not.
	ErrPermanenceNotAllowed = errors.New("this gateway does not allow that to be set to never expire")
	// ErrTokenAlreadyExpired is returned when something asks to make an ALREADY LAPSED token
	// permanent (#2280). Granting that does not extend a credential, it revives one: the holder
	// stopped being able to authenticate on the expiry date and a grant writes expires_at =
	// NULL, so the token works again.
	//
	// 409 rather than 403: nothing about the gateway's configuration forbids it, and the same
	// request a day earlier would have been fine. It is the resource's state that makes it
	// impossible, which is what conflict means.
	ErrTokenAlreadyExpired = errors.New("this token has already expired and cannot be made permanent")
)

// mapErrorToStatusCode converts our service errors into HTTP status codes.
func mapErrorToStatusCode(err error) int {
	if errors.Is(err, ErrUnauthorized) {
		return http.StatusUnauthorized
	}
	if errors.Is(err, ErrForbidden) || errors.Is(err, ErrPermanenceNotAllowed) {
		return http.StatusForbidden
	}
	if errors.Is(err, ErrInvalidRequest) || errors.Is(err, ErrDomainNotSupported) || errors.Is(err, ErrQuotaReached) {
		return http.StatusBadRequest
	}
	if errors.Is(err, ErrNotFound) {
		return http.StatusNotFound
	}
	if errors.Is(err, ErrConflict) || errors.Is(err, ErrQuarantined) || errors.Is(err, ErrTokenAlreadyExpired) {
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
