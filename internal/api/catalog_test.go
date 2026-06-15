package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kite-plus/kite-kvm/internal/catalog"
	"github.com/kite-plus/kite-kvm/internal/config"
)

func testCatalogRouter() http.Handler {
	cfg := &config.Config{
		Flavors: []config.Flavor{{ID: "s1.small", Name: "Small", VCPUs: 1, MemoryMB: 1024, DiskGB: 20}},
		Images:  []config.Image{{ID: "ubuntu-22.04", Name: "Ubuntu 22.04", OSVariant: "ubuntu22.04", BasePath: "/secret/base.img", DefaultUser: "ubuntu"}},
	}
	return NewRouter(Options{
		Auth:    config.Auth{Tokens: []string{"tok"}},
		Catalog: catalog.New(cfg),
	})
}

func TestFlavorsRequireAuth(t *testing.T) {
	r := testCatalogRouter()
	if rec := do(r, http.MethodGet, "/v1/flavors"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("/v1/flavors without token = %d, want 401", rec.Code)
	}
}

func TestFlavorsAndImages(t *testing.T) {
	r := testCatalogRouter()

	req := httptest.NewRequest(http.MethodGet, "/v1/flavors", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/v1/flavors = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"s1.small"`) {
		t.Errorf("flavors body missing flavor id: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/images", nil)
	req.Header.Set("Authorization", "Bearer tok")
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("/v1/images = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"ubuntu-22.04"`) {
		t.Errorf("images body missing image id: %s", body)
	}
	// The internal base path must never be exposed to clients.
	if strings.Contains(body, "base.img") || strings.Contains(body, "base_path") {
		t.Errorf("image base_path leaked in response: %s", body)
	}
}
