package libvirt

import (
	"context"
	"errors"
	"testing"
)

// compile-time assertion that Fake satisfies Conn.
var _ Conn = (*Fake)(nil)

func TestFakeDomainLifecycle(t *testing.T) {
	ctx := context.Background()
	f := NewFake()

	const xml = `<domain type='kvm'><name>kvm-vm1</name></domain>`
	uuid, err := f.DefineDomain(ctx, xml)
	if err != nil {
		t.Fatalf("DefineDomain: %v", err)
	}
	if uuid == "" {
		t.Fatal("expected a uuid")
	}

	if st, _ := f.DomainState(ctx, "kvm-vm1"); st != StateShutoff {
		t.Errorf("defined domain state = %v, want shutoff", st)
	}
	if err := f.StartDomain(ctx, "kvm-vm1"); err != nil {
		t.Fatal(err)
	}
	if st, _ := f.DomainState(ctx, "kvm-vm1"); st != StateRunning {
		t.Errorf("started state = %v, want running", st)
	}
	if err := f.SuspendDomain(ctx, "kvm-vm1"); err != nil {
		t.Fatal(err)
	}
	if st, _ := f.DomainState(ctx, "kvm-vm1"); st != StatePaused {
		t.Errorf("suspended state = %v, want paused", st)
	}
	if err := f.ResumeDomain(ctx, "kvm-vm1"); err != nil {
		t.Fatal(err)
	}
	if err := f.DestroyDomain(ctx, "kvm-vm1"); err != nil {
		t.Fatal(err)
	}
	if st, _ := f.DomainState(ctx, "kvm-vm1"); st != StateShutoff {
		t.Errorf("destroyed state = %v, want shutoff", st)
	}

	names, _ := f.ListDomains(ctx)
	if len(names) != 1 || names[0] != "kvm-vm1" {
		t.Errorf("ListDomains = %v", names)
	}

	if err := f.UndefineDomain(ctx, "kvm-vm1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.DomainState(ctx, "kvm-vm1"); !errors.Is(err, ErrDomainNotFound) {
		t.Errorf("expected ErrDomainNotFound after undefine, got %v", err)
	}
}

func TestFakeMissingDomain(t *testing.T) {
	f := NewFake()
	if err := f.StartDomain(context.Background(), "nope"); !errors.Is(err, ErrDomainNotFound) {
		t.Errorf("expected ErrDomainNotFound, got %v", err)
	}
}

func TestFakeStorageAndDHCP(t *testing.T) {
	ctx := context.Background()
	f := NewFake()

	path, err := f.CreateVolume(ctx, StorageVolSpec{Pool: "default", Name: "vm1.qcow2", CapacityBytes: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	if path == "" || !f.HasVolume("default", "vm1.qcow2") {
		t.Errorf("volume not recorded, path=%q", path)
	}
	if err := f.DeleteVolume(ctx, "default", "vm1.qcow2"); err != nil {
		t.Fatal(err)
	}
	if f.HasVolume("default", "vm1.qcow2") {
		t.Error("volume not deleted")
	}

	if err := f.AddDHCPHost(ctx, "default", "52:54:00:aa:bb:cc", "kvm-vm1", "192.168.122.50"); err != nil {
		t.Fatal(err)
	}
	if ip := f.DHCPHostIP("default", "52:54:00:aa:bb:cc"); ip != "192.168.122.50" {
		t.Errorf("dhcp ip = %q", ip)
	}
	if err := f.RemoveDHCPHost(ctx, "default", "52:54:00:aa:bb:cc"); err != nil {
		t.Fatal(err)
	}
	if ip := f.DHCPHostIP("default", "52:54:00:aa:bb:cc"); ip != "" {
		t.Errorf("dhcp host not removed, ip=%q", ip)
	}
}

func TestFakePing(t *testing.T) {
	f := NewFake()
	if err := f.Ping(context.Background()); err != nil {
		t.Errorf("default Ping = %v", err)
	}
	f.PingErr = errors.New("down")
	if err := f.Ping(context.Background()); err == nil {
		t.Error("expected Ping error when PingErr set")
	}
}
