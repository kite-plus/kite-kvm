package api

import (
	"context"
	"net/http"
)

// ReadyFunc reports whether the agent is ready to serve traffic. It backs the
// /readyz probe; a nil error means ready. Wired to a real libvirt connectivity
// check once the libvirt client exists.
type ReadyFunc func(ctx context.Context) error

type healthHandler struct {
	ready ReadyFunc
}

// handleLive is a liveness probe: the process is up and serving.
func (h *healthHandler) handleLive(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleReady is a readiness probe: dependencies (libvirt) are reachable.
func (h *healthHandler) handleReady(w http.ResponseWriter, r *http.Request) {
	if h.ready != nil {
		if err := h.ready(r.Context()); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"status": "unavailable",
				"error":  err.Error(),
			})
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
