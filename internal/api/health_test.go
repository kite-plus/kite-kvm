package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func do(h http.Handler, method, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestLiveness(t *testing.T) {
	r := NewRouter(Options{})
	if rec := do(r, http.MethodGet, "/healthz"); rec.Code != http.StatusOK {
		t.Fatalf("/healthz = %d, want 200", rec.Code)
	}
}

func TestReadinessOK(t *testing.T) {
	r := NewRouter(Options{Ready: func(context.Context) error { return nil }})
	if rec := do(r, http.MethodGet, "/readyz"); rec.Code != http.StatusOK {
		t.Fatalf("/readyz = %d, want 200", rec.Code)
	}
}

func TestReadinessUnavailable(t *testing.T) {
	r := NewRouter(Options{Ready: func(context.Context) error { return errors.New("libvirt down") }})
	if rec := do(r, http.MethodGet, "/readyz"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("/readyz = %d, want 503", rec.Code)
	}
}
