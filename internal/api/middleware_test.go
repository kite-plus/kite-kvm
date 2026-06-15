package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
}

func TestBearerAuth(t *testing.T) {
	h := bearerAuth([]string{"good-token", "second"})(okHandler())

	cases := []struct {
		name   string
		header string
		want   int
	}{
		{"no header", "", http.StatusUnauthorized},
		{"wrong scheme", "Basic good-token", http.StatusUnauthorized},
		{"wrong token", "Bearer nope", http.StatusUnauthorized},
		{"valid token", "Bearer good-token", http.StatusOK},
		{"valid second token", "Bearer second", http.StatusOK},
		{"case-insensitive scheme", "bearer good-token", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/vms", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestIPAllowlist(t *testing.T) {
	h := ipAllowlist([]string{"10.0.0.0/8", "203.0.113.5"}, nil)(okHandler())

	cases := []struct {
		name   string
		remote string
		want   int
	}{
		{"in cidr", "10.1.2.3:5000", http.StatusOK},
		{"exact ip", "203.0.113.5:443", http.StatusOK},
		{"blocked", "8.8.8.8:1234", http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/vms", nil)
			req.RemoteAddr = tc.remote
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestIPAllowlistEmptyAllowsAll(t *testing.T) {
	h := ipAllowlist(nil, nil)(okHandler())
	req := httptest.NewRequest(http.MethodGet, "/v1/vms", nil)
	req.RemoteAddr = "1.2.3.4:9999"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty allowlist should allow all, got %d", rec.Code)
	}
}
