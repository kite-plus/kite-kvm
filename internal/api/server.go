package api

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/kite-plus/kite-kvm/internal/config"
)

// Server wraps an http.Server with TLS configuration and graceful shutdown.
type Server struct {
	httpServer *http.Server
	cfg        config.Server
	logger     *slog.Logger
}

// NewServer constructs the HTTP server from the server config and handler.
func NewServer(cfg config.Server, handler http.Handler, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		cfg:    cfg,
		logger: logger,
		httpServer: &http.Server{
			Addr:              cfg.Addr,
			Handler:           handler,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
			TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
}

// Run starts serving and blocks until ctx is cancelled or the server fails. On
// cancellation it drains in-flight requests within a bounded shutdown window.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		if s.cfg.Insecure {
			s.logger.Warn("serving without TLS; do not use in production")
			errCh <- s.httpServer.ListenAndServe()
			return
		}
		errCh <- s.httpServer.ListenAndServeTLS(s.cfg.TLSCert, s.cfg.TLSKey)
	}()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		s.logger.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return s.httpServer.Shutdown(shutdownCtx)
	}
}
