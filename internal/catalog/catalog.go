// Package catalog exposes the fixed set of flavors and images that can be
// provisioned and billed, built from configuration. It is the source of truth
// for what a create request may reference.
package catalog

import "github.com/kite-plus/kite-kvm/internal/config"

// Flavor is a sellable resource tier as presented over the API.
type Flavor struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	VCPUs          int    `json:"vcpus"`
	MemoryMB       int    `json:"memory_mb"`
	DiskGB         int    `json:"disk_gb"`
	BandwidthMbps  int    `json:"bandwidth_mbps,omitempty"`
	TrafficQuotaGB int    `json:"traffic_quota_gb,omitempty"`
}

// Image is a base cloud image. BasePath is internal (the read-only golden qcow2)
// and is never serialized to clients.
type Image struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	OSVariant   string `json:"os_variant"`
	DefaultUser string `json:"default_user"`
	BasePath    string `json:"-"`
}

// Catalog holds the provisionable flavors and images.
type Catalog struct {
	flavors    []Flavor
	images     []Image
	flavorByID map[string]Flavor
	imageByID  map[string]Image
}

// New builds a Catalog from the configured flavors and images.
func New(cfg *config.Config) *Catalog {
	c := &Catalog{
		flavors:    make([]Flavor, 0, len(cfg.Flavors)),
		images:     make([]Image, 0, len(cfg.Images)),
		flavorByID: make(map[string]Flavor, len(cfg.Flavors)),
		imageByID:  make(map[string]Image, len(cfg.Images)),
	}
	for _, f := range cfg.Flavors {
		fl := Flavor{
			ID:             f.ID,
			Name:           f.Name,
			VCPUs:          f.VCPUs,
			MemoryMB:       f.MemoryMB,
			DiskGB:         f.DiskGB,
			BandwidthMbps:  f.BandwidthMbps,
			TrafficQuotaGB: f.TrafficQuotaGB,
		}
		c.flavors = append(c.flavors, fl)
		c.flavorByID[fl.ID] = fl
	}
	for _, img := range cfg.Images {
		im := Image{
			ID:          img.ID,
			Name:        img.Name,
			OSVariant:   img.OSVariant,
			DefaultUser: img.DefaultUser,
			BasePath:    img.BasePath,
		}
		c.images = append(c.images, im)
		c.imageByID[im.ID] = im
	}
	return c
}

// Flavors returns all flavors in configured order.
func (c *Catalog) Flavors() []Flavor { return c.flavors }

// Images returns all images in configured order.
func (c *Catalog) Images() []Image { return c.images }

// Flavor looks up a flavor by id.
func (c *Catalog) Flavor(id string) (Flavor, bool) {
	f, ok := c.flavorByID[id]
	return f, ok
}

// Image looks up an image by id.
func (c *Catalog) Image(id string) (Image, bool) {
	im, ok := c.imageByID[id]
	return im, ok
}
