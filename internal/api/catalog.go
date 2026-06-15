package api

import (
	"net/http"

	"github.com/kite-plus/kite-kvm/internal/catalog"
)

type catalogHandler struct {
	catalog *catalog.Catalog
}

func (h *catalogHandler) listFlavors(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"flavors": h.catalog.Flavors()})
}

func (h *catalogHandler) listImages(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"images": h.catalog.Images()})
}
