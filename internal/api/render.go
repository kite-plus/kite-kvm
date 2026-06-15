package api

import (
	"encoding/json"
	"errors"
	"net/http"
)

// writeJSON encodes v as JSON with the given status code. A nil body writes
// only the status line.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// errorEnvelope is the JSON shape of an error response: {"error": {...}}.
type errorEnvelope struct {
	Error *APIError `json:"error"`
}

// writeError renders err as the standard error envelope. Unrecognized errors
// are mapped to a generic 500 so internal details never leak to clients.
func writeError(w http.ResponseWriter, err error) {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		apiErr = errInternal("internal server error")
	}
	writeJSON(w, apiErr.Status, errorEnvelope{Error: apiErr})
}
