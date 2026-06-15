package network

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/kite-plus/kite-kvm/internal/config"
	"github.com/kite-plus/kite-kvm/internal/store"
)

// bridgeStrategy attaches a VM to a host bridge and assigns a public IP from the
// configured pool. The guest is configured statically via cloud-init; no
// host-side wiring is needed because the bridge already exists.
type bridgeStrategy struct {
	cfg   config.Network
	store store.Store
}

func newBridgeStrategy(cfg config.Network, st store.Store) *bridgeStrategy {
	return &bridgeStrategy{cfg: cfg, store: st}
}

func (s *bridgeStrategy) Allocate(ctx context.Context, vmID, hostname string) (Attachment, error) {
	mac, err := generateMAC()
	if err != nil {
		return Attachment{}, err
	}

	candidates, err := expandPool(s.cfg.IPPool, s.cfg.Gateway, maxCandidates)
	if err != nil {
		return Attachment{}, err
	}
	if len(candidates) == 0 {
		return Attachment{}, fmt.Errorf("network %q: ip pool yields no assignable addresses", s.cfg.ID)
	}
	ip, err := s.store.AllocateIP(ctx, s.cfg.ID, vmID, mac, candidates)
	if err != nil {
		return Attachment{}, err // includes store.ErrNoIPAvailable
	}

	prefix := prefixFromNetmask(s.cfg.Netmask)
	return Attachment{
		NetworkID:   s.cfg.ID,
		Mode:        config.NetworkModeBridge,
		MAC:         mac,
		IP:          ip,
		Gateway:     s.cfg.Gateway,
		Netmask:     s.cfg.Netmask,
		Source:      s.cfg.Bridge,
		VLAN:        s.cfg.VLAN,
		Static:      true,
		AddressCIDR: ip + "/" + prefix,
		Nameservers: s.cfg.DNS,
	}, nil
}

func (s *bridgeStrategy) Release(ctx context.Context, vmID, mac string) error {
	return s.store.ReleaseIPByVM(ctx, vmID)
}

// expandPool expands the pool entries (plain IPs and CIDRs) into an ordered,
// de-duplicated candidate list, excluding the gateway address.
func expandPool(entries []string, gateway string, max int) ([]string, error) {
	seen := make(map[string]struct{})
	var out []string
	add := func(ip string) {
		if ip == gateway {
			return
		}
		if _, ok := seen[ip]; ok {
			return
		}
		seen[ip] = struct{}{}
		out = append(out, ip)
	}
	for _, e := range entries {
		if len(out) >= max {
			break
		}
		if strings.Contains(e, "/") {
			hosts, err := expandCIDRHosts(e)
			if err != nil {
				return nil, err
			}
			for _, h := range hosts {
				add(h)
				if len(out) >= max {
					break
				}
			}
			continue
		}
		if net.ParseIP(e) == nil {
			return nil, fmt.Errorf("invalid pool ip %q", e)
		}
		add(e)
	}
	return out, nil
}

// expandCIDRHosts returns the assignable addresses of an IPv4 CIDR. For prefixes
// of /30 and shorter it excludes the network and broadcast addresses; /31 and
// /32 (point-to-point / host) include every address.
func expandCIDRHosts(cidr string) ([]string, error) {
	_, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}
	base := ipnet.IP.To4()
	if base == nil {
		return nil, fmt.Errorf("network: only IPv4 pools are supported, got %q", cidr)
	}
	ones, bits := ipnet.Mask.Size()
	netU := ip4ToU32(base)
	size := uint32(1) << uint(bits-ones)

	var lo, hi uint32
	if ones >= 31 {
		lo, hi = netU, netU+size-1
	} else {
		lo, hi = netU+1, netU+size-2
	}
	var hosts []string
	for u := lo; u <= hi; u++ {
		hosts = append(hosts, u32ToIP4(u).String())
	}
	return hosts, nil
}

// prefixFromNetmask converts a netmask ("255.255.255.0") or prefix ("/24") into
// a bare prefix length string ("24"). Unknown input defaults to "32".
func prefixFromNetmask(nm string) string {
	nm = strings.TrimSpace(nm)
	if nm == "" {
		return "32"
	}
	if strings.HasPrefix(nm, "/") {
		return strings.TrimPrefix(nm, "/")
	}
	ip := net.ParseIP(nm).To4()
	if ip == nil {
		return "32"
	}
	ones, _ := net.IPMask(ip).Size()
	return strconv.Itoa(ones)
}
