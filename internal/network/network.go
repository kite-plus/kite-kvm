// Package network allocates a VM's MAC and IP and applies the host-side wiring
// for its attachment mode. NAT and bridge are pluggable strategies selected per
// VM; the Manager dispatches by network id. IPv4 only for the initial version.
package network

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"

	"github.com/kite-plus/kite-kvm/internal/config"
	"github.com/kite-plus/kite-kvm/internal/libvirt"
	"github.com/kite-plus/kite-kvm/internal/store"
)

// ErrNetworkNotFound is returned for an unknown network id.
var ErrNetworkNotFound = errors.New("network: not found")

// maxCandidates bounds how many host IPs are offered to the allocator for a
// single request, so a large subnet does not produce an unbounded list.
const maxCandidates = 4096

// Attachment is the result of allocating a VM onto a network. It carries
// everything the domain XML and cloud-init need.
type Attachment struct {
	NetworkID   string
	Mode        string // nat | bridge
	MAC         string
	IP          string
	Gateway     string
	Netmask     string
	Source      string   // nat: libvirt network name; bridge: host bridge device
	VLAN        int      // bridge only
	Static      bool     // bridge => static cloud-init config; nat => DHCP
	AddressCIDR string   // static only, e.g. "203.0.113.10/24"
	Nameservers []string // static only
}

// Strategy allocates and releases a VM on a specific configured network.
type Strategy interface {
	Allocate(ctx context.Context, vmID, hostname string) (Attachment, error)
	Release(ctx context.Context, vmID, mac string) error
}

// Manager holds one Strategy per configured network and dispatches by id.
type Manager struct {
	strategies map[string]Strategy
}

// NewManager builds strategies for every configured network.
func NewManager(cfg *config.Config, st store.Store, conn libvirt.Conn) (*Manager, error) {
	m := &Manager{strategies: make(map[string]Strategy, len(cfg.Networks))}
	for i := range cfg.Networks {
		n := cfg.Networks[i]
		switch n.Mode {
		case config.NetworkModeNAT:
			m.strategies[n.ID] = newNATStrategy(n, st, conn)
		case config.NetworkModeBridge:
			m.strategies[n.ID] = newBridgeStrategy(n, st)
		default:
			return nil, fmt.Errorf("network %q: unsupported mode %q", n.ID, n.Mode)
		}
	}
	return m, nil
}

// Allocate places a VM onto the named network.
func (m *Manager) Allocate(ctx context.Context, networkID, vmID, hostname string) (Attachment, error) {
	s, ok := m.strategies[networkID]
	if !ok {
		return Attachment{}, fmt.Errorf("%w: %s", ErrNetworkNotFound, networkID)
	}
	return s.Allocate(ctx, vmID, hostname)
}

// Release frees a VM's allocation on the named network. A missing network is
// not an error (it may have been removed from config after provisioning).
func (m *Manager) Release(ctx context.Context, networkID, vmID, mac string) error {
	s, ok := m.strategies[networkID]
	if !ok {
		return nil
	}
	return s.Release(ctx, vmID, mac)
}

// --- helpers ---------------------------------------------------------------

// generateMAC returns a locally-administered QEMU MAC (52:54:00:xx:xx:xx).
func generateMAC() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return fmt.Sprintf("52:54:00:%02x:%02x:%02x", b[0], b[1], b[2]), nil
}

func ip4ToU32(ip net.IP) uint32 {
	ip = ip.To4()
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

func u32ToIP4(u uint32) net.IP {
	return net.IPv4(byte(u>>24), byte(u>>16), byte(u>>8), byte(u)).To4()
}

// subnetHosts returns the gateway (first usable address) and up to max usable
// host addresses of an IPv4 CIDR, excluding the network, gateway, and broadcast
// addresses.
func subnetHosts(cidr string, max int) (gateway net.IP, hosts []string, err error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, nil, err
	}
	base := ipnet.IP.To4()
	if base == nil {
		return nil, nil, fmt.Errorf("network: only IPv4 subnets are supported, got %q", cidr)
	}
	ones, bits := ipnet.Mask.Size()
	netU := ip4ToU32(base)
	size := uint32(1) << uint(bits-ones)
	gwU := netU + 1
	bcastU := netU + size - 1
	gw := u32ToIP4(gwU)
	for u := netU + 2; u < bcastU && len(hosts) < max; u++ {
		hosts = append(hosts, u32ToIP4(u).String())
	}
	return gw, hosts, nil
}

func netmaskOf(cidr string) string {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return ""
	}
	m := ipnet.Mask
	if len(m) != 4 {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d.%d", m[0], m[1], m[2], m[3])
}

// sanitizeHostname reduces a string to a DHCP-safe hostname, falling back to a
// VM-id-derived name.
func sanitizeHostname(hostname, vmID string) string {
	clean := keepHostChars(hostname)
	if clean == "" {
		clean = "vm-" + keepHostChars(vmID)
	}
	if len(clean) > 63 {
		clean = clean[:63]
	}
	return clean
}

func keepHostChars(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			out = append(out, r)
		}
	}
	return string(out)
}
