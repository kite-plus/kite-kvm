// Package api wires the HTTP layer: routing, middleware, request handling, and
// the TLS server lifecycle. It owns the /v1 REST resource model.
package api

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/kite-plus/kite-kvm/internal/config"
)

// Options carries the router's dependencies.
type Options struct {
	Logger *slog.Logger
	Ready  ReadyFunc
	Auth   config.Auth
}

// NewRouter builds the HTTP handler with the base middleware chain, the health
// probes at the root, and the (initially empty) /v1 API surface.
func NewRouter(opts Options) http.Handler {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(requestLogger(logger))
	r.Use(middleware.Recoverer)

	h := &healthHandler{ready: opts.Ready}
	r.Get("/healthz", h.handleLive)
	r.Get("/readyz", h.handleReady)

	r.Route("/v1", func(r chi.Router) {
		// Every /v1 endpoint is gated by the source allowlist and a bearer
		// token. Health probes above stay unauthenticated for load balancers.
		r.Use(ipAllowlist(opts.Auth.IPAllowlist, logger))
		r.Use(bearerAuth(opts.Auth.Tokens))
		// Resource routes are mounted by later commits.
	})

	return r
}

// requestLogger logs one structured line per request using slog.
func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", ww.Status(),
				"bytes", ww.BytesWritten(),
				"duration_ms", time.Since(start).Milliseconds(),
				"request_id", middleware.GetReqID(r.Context()),
				"remote", r.RemoteAddr,
			)
		})
	}
}
