package network

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/kite-plus/kite-kvm/internal/config"
	"github.com/kite-plus/kite-kvm/internal/store"
)

func testBridgeManager(t *testing.T) *Manager {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	cfg := &config.Config{Networks: []config.Network{{
		ID:      "public-1",
		Mode:    config.NetworkModeBridge,
		Bridge:  "br0",
		Gateway: "203.0.113.1",
		Netmask: "255.255.255.0",
		DNS:     []string{"1.1.1.1"},
		IPPool:  []string{"203.0.113.10", "203.0.113.12/31"}, // .10, .12, .13
	}}}
	m, err := NewManager(cfg, st, nil) // bridge needs no libvirt conn
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func TestBridgeAllocate(t *testing.T) {
	m := testBridgeManager(t)
	ctx := context.Background()

	att, err := m.Allocate(ctx, "public-1", "vm1", "web1")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	if att.IP != "203.0.113.10" {
		t.Errorf("first IP = %s, want .10", att.IP)
	}
	if !att.Static {
		t.Error("bridge attachment should be static")
	}
	if att.AddressCIDR != "203.0.113.10/24" {
		t.Errorf("AddressCIDR = %s, want 203.0.113.10/24", att.AddressCIDR)
	}
	if att.Source != "br0" || att.Gateway != "203.0.113.1" {
		t.Errorf("unexpected attachment %+v", att)
	}
	if len(att.Nameservers) != 1 || att.Nameservers[0] != "1.1.1.1" {
		t.Errorf("nameservers = %v", att.Nameservers)
	}
}

func TestBridgePoolExhaustion(t *testing.T) {
	m := testBridgeManager(t)
	ctx := context.Background()
	// Pool yields exactly 3 addresses (.10, .12, .13); the gateway .1 is excluded.
	for i, vm := range []string{"vm1", "vm2", "vm3"} {
		if _, err := m.Allocate(ctx, "public-1", vm, vm); err != nil {
			t.Fatalf("alloc %d: %v", i, err)
		}
	}
	if _, err := m.Allocate(ctx, "public-1", "vm4", "vm4"); !errors.Is(err, store.ErrNoIPAvailable) {
		t.Errorf("expected ErrNoIPAvailable when pool exhausted, got %v", err)
	}
}

func TestExpandPoolExcludesGateway(t *testing.T) {
	hosts, err := expandPool([]string{"203.0.113.0/29"}, "203.0.113.1", 100)
	if err != nil {
		t.Fatal(err)
	}
	// /29 usable hosts are .1-.6; the gateway .1 is excluded -> .2-.6 (5).
	if len(hosts) != 5 {
		t.Fatalf("hosts = %v (want 5)", hosts)
	}
	for _, h := range hosts {
		if h == "203.0.113.1" {
			t.Error("gateway should be excluded from pool")
		}
	}
}

func TestPrefixFromNetmask(t *testing.T) {
	cases := map[string]string{
		"255.255.255.0":   "24",
		"255.255.255.128": "25",
		"/30":             "30",
		"":                "32",
	}
	for in, want := range cases {
		if got := prefixFromNetmask(in); got != want {
			t.Errorf("prefixFromNetmask(%q) = %s, want %s", in, got, want)
		}
	}
}
