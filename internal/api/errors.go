package api

import "net/http"

// APIError is a typed API error that carries the HTTP status, a stable machine
// code, and a human-readable message. It is rendered as the JSON error
// envelope by writeError.
type APIError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

func newAPIError(status int, code, msg string) *APIError {
	return &APIError{Status: status, Code: code, Message: msg}
}

// Constructors for the common error classes. Codes are stable identifiers that
// clients (and the billing system) may switch on.
func errBadRequest(msg string) *APIError {
	return newAPIError(http.StatusBadRequest, "bad_request", msg)
}
func errUnauthorized(msg string) *APIError {
	return newAPIError(http.StatusUnauthorized, "unauthorized", msg)
}
func errForbidden(msg string) *APIError { return newAPIError(http.StatusForbidden, "forbidden", msg) }
func errNotFound(msg string) *APIError  { return newAPIError(http.StatusNotFound, "not_found", msg) }
func errConflict(msg string) *APIError  { return newAPIError(http.StatusConflict, "conflict", msg) }
func errUnprocessable(msg string) *APIError {
	return newAPIError(http.StatusUnprocessableEntity, "unprocessable", msg)
}
func errNotImplemented(msg string) *APIError {
	return newAPIError(http.StatusNotImplemented, "not_implemented", msg)
}
func errInternal(msg string) *APIError {
	return newAPIError(http.StatusInternalServerError, "internal", msg)
}
