package network

import (
	"context"
	"fmt"

	"github.com/kite-plus/kite-kvm/internal/config"
	"github.com/kite-plus/kite-kvm/internal/libvirt"
	"github.com/kite-plus/kite-kvm/internal/store"
)

// natStrategy allocates a private IP from the libvirt NAT network's subnet,
// pins it to the VM's MAC with a DHCP static lease, and the guest receives it
// over DHCP.
type natStrategy struct {
	cfg   config.Network
	store store.Store
	conn  libvirt.Conn
}

func newNATStrategy(cfg config.Network, st store.Store, conn libvirt.Conn) *natStrategy {
	return &natStrategy{cfg: cfg, store: st, conn: conn}
}

func (s *natStrategy) Allocate(ctx context.Context, vmID, hostname string) (Attachment, error) {
	mac, err := generateMAC()
	if err != nil {
		return Attachment{}, err
	}
	if s.cfg.Subnet == "" {
		return Attachment{}, fmt.Errorf("network %q: subnet is required to assign a static lease", s.cfg.ID)
	}

	gateway, hosts, err := subnetHosts(s.cfg.Subnet, maxCandidates)
	if err != nil {
		return Attachment{}, err
	}
	ip, err := s.store.AllocateIP(ctx, s.cfg.ID, vmID, mac, hosts)
	if err != nil {
		return Attachment{}, err // includes store.ErrNoIPAvailable
	}

	name := sanitizeHostname(hostname, vmID)
	if err := s.conn.AddDHCPHost(ctx, s.cfg.LibvirtNetwork, mac, name, ip); err != nil {
		// Roll back the reservation so the IP is not leaked.
		_ = s.store.ReleaseIPByVM(ctx, vmID)
		return Attachment{}, fmt.Errorf("add dhcp lease: %w", err)
	}

	return Attachment{
		NetworkID: s.cfg.ID,
		Mode:      config.NetworkModeNAT,
		MAC:       mac,
		IP:        ip,
		Gateway:   gateway.String(),
		Netmask:   netmaskOf(s.cfg.Subnet),
		Source:    s.cfg.LibvirtNetwork,
		Static:    false, // guest configures via DHCP
	}, nil
}

func (s *natStrategy) Release(ctx context.Context, vmID, mac string) error {
	var firstErr error
	if mac != "" {
		if err := s.conn.RemoveDHCPHost(ctx, s.cfg.LibvirtNetwork, mac); err != nil {
			firstErr = err
		}
	}
	if err := s.store.ReleaseIPByVM(ctx, vmID); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}
