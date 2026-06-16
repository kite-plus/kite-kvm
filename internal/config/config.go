// Package config loads, defaults, and validates the kite-kvm agent
// configuration from a YAML file with selective environment-variable overrides.
package config

import (
	"fmt"
	"net"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Network modes.
const (
	NetworkModeNAT    = "nat"
	NetworkModeBridge = "bridge"
)

// Config is the root agent configuration.
type Config struct {
	Server   Server    `yaml:"server"`
	Auth     Auth      `yaml:"auth"`
	Libvirt  Libvirt   `yaml:"libvirt"`
	Storage  Storage   `yaml:"storage"`
	Traffic  Traffic   `yaml:"traffic"`
	Networks []Network `yaml:"networks"`
	Flavors  []Flavor  `yaml:"flavors"`
	Images   []Image   `yaml:"images"`
}

// Traffic holds the traffic-accounting poller settings.
type Traffic struct {
	// IntervalSeconds is how often usage is sampled and quotas enforced.
	IntervalSeconds int `yaml:"interval_seconds"`
}

// Server holds HTTP listener and TLS settings.
type Server struct {
	Addr    string `yaml:"addr"`     // listen address, e.g. ":8443"
	TLSCert string `yaml:"tls_cert"` // path to PEM certificate
	TLSKey  string `yaml:"tls_key"`  // path to PEM private key
	// Insecure disables TLS. Intended for local development only; production
	// deployments must serve over TLS.
	Insecure bool `yaml:"insecure"`
}

// Auth holds API authentication settings.
type Auth struct {
	// Tokens are accepted bearer tokens. At least one is required.
	Tokens []string `yaml:"tokens"`
	// IPAllowlist restricts which client IPs may call the API. Entries may be
	// plain IPs or CIDR blocks. An empty list allows all sources.
	IPAllowlist []string `yaml:"ip_allowlist"`
}

// Libvirt holds connection and storage-layout settings for the hypervisor.
type Libvirt struct {
	URI          string `yaml:"uri"`            // e.g. qemu:///system
	StoragePool  string `yaml:"storage_pool"`   // libvirt storage pool name
	ImageBaseDir string `yaml:"image_base_dir"` // read-only golden images live here
	InstanceDir  string `yaml:"instance_dir"`   // per-VM overlays and seed ISOs live here
}

// Storage holds local agent persistence settings.
type Storage struct {
	StatePath string `yaml:"state_path"` // SQLite database path
}

// Network declares one provisionable network. NAT networks map to a libvirt
// network (virbr0). Bridge networks attach to a host bridge and assign public
// IPs from a pool.
type Network struct {
	ID      string `yaml:"id"`
	Mode    string `yaml:"mode"`    // nat | bridge
	Default bool   `yaml:"default"` // used when a create request omits a network

	// NAT mode.
	LibvirtNetwork string `yaml:"libvirt_network"` // e.g. "default"
	Subnet         string `yaml:"subnet"`          // e.g. "192.168.122.0/24"

	// Bridge mode.
	Bridge  string   `yaml:"bridge"`  // host bridge, e.g. "br0"
	IPPool  []string `yaml:"ip_pool"` // assignable public IPs (plain IPs or CIDRs)
	Gateway string   `yaml:"gateway"`
	Netmask string   `yaml:"netmask"` // e.g. "255.255.255.0" or prefix "/24"
	DNS     []string `yaml:"dns"`
	VLAN    int      `yaml:"vlan"` // optional 802.1Q tag, 0 = untagged
}

// Flavor is a sellable resource tier.
type Flavor struct {
	ID             string `yaml:"id"`
	Name           string `yaml:"name"`
	VCPUs          int    `yaml:"vcpus"`
	MemoryMB       int    `yaml:"memory_mb"`
	DiskGB         int    `yaml:"disk_gb"`
	BandwidthMbps  int    `yaml:"bandwidth_mbps"`   // optional rate cap, 0 = unlimited
	TrafficQuotaGB int    `yaml:"traffic_quota_gb"` // combined in+out transfer cap, 0 = unlimited
}

// Image is a base (golden) cloud image VMs are provisioned from.
type Image struct {
	ID          string `yaml:"id"`
	Name        string `yaml:"name"`
	OSVariant   string `yaml:"os_variant"`   // e.g. "ubuntu22.04"
	BasePath    string `yaml:"base_path"`    // path to the read-only golden qcow2
	DefaultUser string `yaml:"default_user"` // cloud image default login, e.g. "ubuntu"
}

// Load reads, defaults, env-overrides, and validates the config at path.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.applyEnv()
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	return &cfg, nil
}

// applyEnv overlays a small set of operationally sensitive values from the
// environment (handy for secrets and container deployments).
func (c *Config) applyEnv() {
	if v := os.Getenv("KITE_SERVER_ADDR"); v != "" {
		c.Server.Addr = v
	}
	if v := os.Getenv("KITE_TLS_CERT"); v != "" {
		c.Server.TLSCert = v
	}
	if v := os.Getenv("KITE_TLS_KEY"); v != "" {
		c.Server.TLSKey = v
	}
	if v := os.Getenv("KITE_AUTH_TOKENS"); v != "" {
		c.Auth.Tokens = splitAndTrim(v)
	}
	if v := os.Getenv("KITE_LIBVIRT_URI"); v != "" {
		c.Libvirt.URI = v
	}
	if v := os.Getenv("KITE_STATE_PATH"); v != "" {
		c.Storage.StatePath = v
	}
}

// applyDefaults fills unset fields with sensible defaults.
func (c *Config) applyDefaults() {
	if c.Server.Addr == "" {
		c.Server.Addr = ":8443"
	}
	if c.Libvirt.URI == "" {
		c.Libvirt.URI = "qemu:///system"
	}
	if c.Libvirt.StoragePool == "" {
		c.Libvirt.StoragePool = "default"
	}
	if c.Libvirt.ImageBaseDir == "" {
		c.Libvirt.ImageBaseDir = "/var/lib/libvirt/images/base"
	}
	if c.Libvirt.InstanceDir == "" {
		c.Libvirt.InstanceDir = "/var/lib/libvirt/images"
	}
	if c.Storage.StatePath == "" {
		c.Storage.StatePath = "/var/lib/kite-kvm/state.db"
	}
	if c.Traffic.IntervalSeconds <= 0 {
		c.Traffic.IntervalSeconds = 60
	}
}

// Validate checks the config for completeness and consistency.
func (c *Config) Validate() error {
	if !c.Server.Insecure {
		if c.Server.TLSCert == "" || c.Server.TLSKey == "" {
			return fmt.Errorf("server.tls_cert and server.tls_key are required unless server.insecure is set")
		}
	}
	if len(c.Auth.Tokens) == 0 {
		return fmt.Errorf("auth.tokens must contain at least one token")
	}
	for i, tok := range c.Auth.Tokens {
		if strings.TrimSpace(tok) == "" {
			return fmt.Errorf("auth.tokens[%d] is empty", i)
		}
	}
	for i, entry := range c.Auth.IPAllowlist {
		if err := validateCIDROrIP(entry); err != nil {
			return fmt.Errorf("auth.ip_allowlist[%d]: %w", i, err)
		}
	}

	if err := c.validateNetworks(); err != nil {
		return err
	}
	if err := c.validateFlavors(); err != nil {
		return err
	}
	return c.validateImages()
}

func (c *Config) validateNetworks() error {
	if len(c.Networks) == 0 {
		return fmt.Errorf("at least one network must be configured")
	}
	seen := make(map[string]bool, len(c.Networks))
	defaults := 0
	for i := range c.Networks {
		n := &c.Networks[i]
		if n.ID == "" {
			return fmt.Errorf("networks[%d].id is required", i)
		}
		if seen[n.ID] {
			return fmt.Errorf("duplicate network id %q", n.ID)
		}
		seen[n.ID] = true
		if n.Default {
			defaults++
		}
		switch n.Mode {
		case NetworkModeNAT:
			if n.LibvirtNetwork == "" {
				return fmt.Errorf("network %q (nat): libvirt_network is required", n.ID)
			}
			if n.Subnet != "" {
				if _, _, err := net.ParseCIDR(n.Subnet); err != nil {
					return fmt.Errorf("network %q (nat): invalid subnet %q", n.ID, n.Subnet)
				}
			}
		case NetworkModeBridge:
			if n.Bridge == "" {
				return fmt.Errorf("network %q (bridge): bridge is required", n.ID)
			}
			if len(n.IPPool) == 0 {
				return fmt.Errorf("network %q (bridge): ip_pool must not be empty", n.ID)
			}
			if n.Gateway == "" {
				return fmt.Errorf("network %q (bridge): gateway is required", n.ID)
			}
			for j, p := range n.IPPool {
				if err := validateCIDROrIP(p); err != nil {
					return fmt.Errorf("network %q (bridge): ip_pool[%d]: %w", n.ID, j, err)
				}
			}
		default:
			return fmt.Errorf("network %q: mode must be %q or %q, got %q", n.ID, NetworkModeNAT, NetworkModeBridge, n.Mode)
		}
	}
	if defaults > 1 {
		return fmt.Errorf("at most one network may be marked default")
	}
	return nil
}

func (c *Config) validateFlavors() error {
	if len(c.Flavors) == 0 {
		return fmt.Errorf("at least one flavor must be configured")
	}
	seen := make(map[string]bool, len(c.Flavors))
	for i := range c.Flavors {
		f := &c.Flavors[i]
		if f.ID == "" {
			return fmt.Errorf("flavors[%d].id is required", i)
		}
		if seen[f.ID] {
			return fmt.Errorf("duplicate flavor id %q", f.ID)
		}
		seen[f.ID] = true
		if f.VCPUs <= 0 {
			return fmt.Errorf("flavor %q: vcpus must be > 0", f.ID)
		}
		if f.MemoryMB <= 0 {
			return fmt.Errorf("flavor %q: memory_mb must be > 0", f.ID)
		}
		if f.DiskGB <= 0 {
			return fmt.Errorf("flavor %q: disk_gb must be > 0", f.ID)
		}
		if f.BandwidthMbps < 0 {
			return fmt.Errorf("flavor %q: bandwidth_mbps must be >= 0", f.ID)
		}
		if f.TrafficQuotaGB < 0 {
			return fmt.Errorf("flavor %q: traffic_quota_gb must be >= 0", f.ID)
		}
	}
	return nil
}

func (c *Config) validateImages() error {
	if len(c.Images) == 0 {
		return fmt.Errorf("at least one image must be configured")
	}
	seen := make(map[string]bool, len(c.Images))
	for i := range c.Images {
		img := &c.Images[i]
		if img.ID == "" {
			return fmt.Errorf("images[%d].id is required", i)
		}
		if seen[img.ID] {
			return fmt.Errorf("duplicate image id %q", img.ID)
		}
		seen[img.ID] = true
		if img.BasePath == "" {
			return fmt.Errorf("image %q: base_path is required", img.ID)
		}
	}
	return nil
}

// NetworkByID returns the configured network with the given id, or nil.
func (c *Config) NetworkByID(id string) *Network {
	for i := range c.Networks {
		if c.Networks[i].ID == id {
			return &c.Networks[i]
		}
	}
	return nil
}

// DefaultNetwork returns the network flagged default, or the first network if
// none is flagged. Networks is guaranteed non-empty after Validate.
func (c *Config) DefaultNetwork() *Network {
	for i := range c.Networks {
		if c.Networks[i].Default {
			return &c.Networks[i]
		}
	}
	if len(c.Networks) > 0 {
		return &c.Networks[0]
	}
	return nil
}

func validateCIDROrIP(s string) error {
	if strings.Contains(s, "/") {
		if _, _, err := net.ParseCIDR(s); err != nil {
			return fmt.Errorf("invalid CIDR %q", s)
		}
		return nil
	}
	if net.ParseIP(s) == nil {
		return fmt.Errorf("invalid IP %q", s)
	}
	return nil
}

func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
