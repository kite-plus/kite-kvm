package api

import (
	"encoding/json"
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
