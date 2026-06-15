package network

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/kite-plus/kite-kvm/internal/config"
	"github.com/kite-plus/kite-kvm/internal/libvirt"
	"github.com/kite-plus/kite-kvm/internal/store"
)

func testManager(t *testing.T) (*Manager, *libvirt.Fake, store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	conn := libvirt.NewFake()
	cfg := &config.Config{Networks: []config.Network{{
		ID:             "nat-default",
		Mode:           config.NetworkModeNAT,
		LibvirtNetwork: "default",
		Subnet:         "192.168.122.0/24",
	}}}
	m, err := NewManager(cfg, st, conn)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m, conn, st
}

func TestNATAllocate(t *testing.T) {
	m, conn, _ := testManager(t)
	ctx := context.Background()

	att, err := m.Allocate(ctx, "nat-default", "vm1", "web1")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if att.IP != "192.168.122.2" {
		t.Errorf("first IP = %s, want 192.168.122.2", att.IP)
	}
	if att.Gateway != "192.168.122.1" || att.Netmask != "255.255.255.0" {
		t.Errorf("gateway/netmask = %s / %s", att.Gateway, att.Netmask)
	}
	if att.Source != "default" || att.Static {
		t.Errorf("unexpected attachment %+v", att)
	}
	if len(att.MAC) != 17 || att.MAC[:9] != "52:54:00:" {
		t.Errorf("bad MAC %q", att.MAC)
	}
	if ip := conn.DHCPHostIP("default", att.MAC); ip != att.IP {
		t.Errorf("dhcp lease IP = %q, want %q", ip, att.IP)
	}

	att2, err := m.Allocate(ctx, "nat-default", "vm2", "web2")
	if err != nil {
		t.Fatal(err)
	}
	if att2.IP != "192.168.122.3" {
		t.Errorf("second IP = %s, want .3", att2.IP)
	}
}

func TestNATReleaseReclaims(t *testing.T) {
	m, conn, _ := testManager(t)
	ctx := context.Background()

	a1, _ := m.Allocate(ctx, "nat-default", "vm1", "h1")
	if err := m.Release(ctx, "nat-default", "vm1", a1.MAC); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if ip := conn.DHCPHostIP("default", a1.MAC); ip != "" {
		t.Errorf("dhcp lease not removed, ip=%q", ip)
	}
	a2, err := m.Allocate(ctx, "nat-default", "vm2", "h2")
	if err != nil {
		t.Fatal(err)
	}
	if a2.IP != a1.IP {
		t.Errorf("released IP not reclaimed: got %s, want %s", a2.IP, a1.IP)
	}
}

func TestUnknownNetwork(t *testing.T) {
	m, _, _ := testManager(t)
	if _, err := m.Allocate(context.Background(), "nope", "vm1", "h"); !errors.Is(err, ErrNetworkNotFound) {
		t.Errorf("expected ErrNetworkNotFound, got %v", err)
	}
}

func TestBridgeModeRejectedForNow(t *testing.T) {
	cfg := &config.Config{Networks: []config.Network{{
		ID: "public-1", Mode: config.NetworkModeBridge, Bridge: "br0",
		Gateway: "203.0.113.1", IPPool: []string{"203.0.113.10"},
	}}}
	if _, err := NewManager(cfg, nil, nil); err == nil {
		t.Error("expected bridge mode to be unsupported until C12")
	}
}
