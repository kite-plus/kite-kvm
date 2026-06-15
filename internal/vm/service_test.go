package vm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kite-plus/kite-kvm/internal/catalog"
	"github.com/kite-plus/kite-kvm/internal/config"
	"github.com/kite-plus/kite-kvm/internal/job"
	"github.com/kite-plus/kite-kvm/internal/libvirt"
	"github.com/kite-plus/kite-kvm/internal/model"
	"github.com/kite-plus/kite-kvm/internal/network"
	"github.com/kite-plus/kite-kvm/internal/provision"
	"github.com/kite-plus/kite-kvm/internal/store"
)

func testService(t *testing.T) (*Service, *libvirt.Fake, store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	conn := libvirt.NewFake()
	conn.BaseDir = dir

	cfg := &config.Config{
		Libvirt: config.Libvirt{StoragePool: "default", InstanceDir: dir},
		Networks: []config.Network{
			{ID: "nat-default", Mode: config.NetworkModeNAT, Default: true, LibvirtNetwork: "default", Subnet: "192.168.122.0/24"},
			{ID: "public-1", Mode: config.NetworkModeBridge, Bridge: "br0", Gateway: "203.0.113.1", Netmask: "255.255.255.0", IPPool: []string{"203.0.113.10", "203.0.113.11"}},
		},
		Flavors: []config.Flavor{{ID: "s1.small", Name: "Small", VCPUs: 1, MemoryMB: 1024, DiskGB: 20, BandwidthMbps: 100}},
		Images:  []config.Image{{ID: "ubuntu-22.04", Name: "Ubuntu", OSVariant: "ubuntu22.04", BasePath: "/base/jammy.img", DefaultUser: "ubuntu"}},
	}
	netmgr, err := network.NewManager(cfg, st, conn)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	prov := provision.NewProvisioner(conn, "default", dir)
	q := job.NewQueue(st, 2, nil)
	svc := NewService(cfg, st, conn, catalog.New(cfg), netmgr, prov, q, nil)
	q.Start(context.Background())
	t.Cleanup(q.Stop)
	return svc, conn, st
}

func waitVM(t *testing.T, st store.Store, id string) *model.VM {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		vm, err := st.GetVM(context.Background(), id)
		if err != nil {
			t.Fatalf("GetVM: %v", err)
		}
		if vm.Status == model.VMStatusRunning || vm.Status == model.VMStatusError {
			return vm
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("vm did not finish provisioning")
	return nil
}

func TestCreateNAT(t *testing.T) {
	svc, conn, st := testService(t)
	ctx := context.Background()

	j, err := svc.Create(ctx, CreateRequest{
		FlavorID: "s1.small",
		ImageID:  "ubuntu-22.04",
		Hostname: "web1",
		Password: "secret",
		SSHKeys:  []string{"ssh-ed25519 AAAA"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if j.Type != model.JobCreate {
		t.Errorf("job type = %s", j.Type)
	}

	vm := waitVM(t, st, j.VMID)
	if vm.Status != model.VMStatusRunning {
		t.Fatalf("status = %s, want running", vm.Status)
	}
	if vm.IP != "192.168.122.2" {
		t.Errorf("IP = %s, want 192.168.122.2", vm.IP)
	}
	if vm.MAC == "" || vm.DomainUUID == "" {
		t.Errorf("missing mac/uuid: %+v", vm)
	}
	if !conn.HasDomain(vm.DomainName) {
		t.Error("domain not defined")
	}
	if state, _ := conn.DomainState(ctx, vm.DomainName); state != libvirt.StateRunning {
		t.Errorf("domain state = %v, want running", state)
	}
	if !conn.HasVolume("default", vm.ID+".qcow2") {
		t.Error("overlay volume not created")
	}
	if ip := conn.DHCPHostIP("default", vm.MAC); ip != vm.IP {
		t.Errorf("dhcp lease = %q, want %q", ip, vm.IP)
	}
}

func TestCreateBridge(t *testing.T) {
	svc, conn, st := testService(t)
	ctx := context.Background()

	j, err := svc.Create(ctx, CreateRequest{
		FlavorID: "s1.small",
		ImageID:  "ubuntu-22.04",
		Network:  NetworkRequest{Mode: "bridge"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	vm := waitVM(t, st, j.VMID)
	if vm.Status != model.VMStatusRunning {
		t.Fatalf("status = %s, want running", vm.Status)
	}
	if vm.NetworkMode != model.NetworkBridge {
		t.Errorf("network mode = %s, want bridge", vm.NetworkMode)
	}
	if vm.IP != "203.0.113.10" {
		t.Errorf("IP = %s, want 203.0.113.10", vm.IP)
	}
	if !conn.HasDomain(vm.DomainName) {
		t.Error("domain not defined")
	}
}

func TestListGetStatus(t *testing.T) {
	svc, _, st := testService(t)
	ctx := context.Background()

	j, err := svc.Create(ctx, CreateRequest{FlavorID: "s1.small", ImageID: "ubuntu-22.04"})
	if err != nil {
		t.Fatal(err)
	}
	waitVM(t, st, j.VMID)

	list, err := svc.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v len=%d", err, len(list))
	}

	got, err := svc.Get(ctx, j.VMID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.PowerState != model.PowerRunning {
		t.Errorf("reconciled power state = %s, want running", got.PowerState)
	}

	info, err := svc.Status(ctx, j.VMID)
	if err != nil {
		t.Fatal(err)
	}
	if info.Status != model.VMStatusRunning || info.PowerState != model.PowerRunning {
		t.Errorf("status = %+v", info)
	}

	if _, err := svc.Get(ctx, "ghost"); !errors.Is(err, ErrVMNotFound) {
		t.Errorf("expected ErrVMNotFound, got %v", err)
	}
}

func waitJob(t *testing.T, st store.Store, id string) *model.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, err := st.GetJob(context.Background(), id)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if j.State == model.JobSucceeded || j.State == model.JobFailed {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return nil
}

func TestPowerOperations(t *testing.T) {
	svc, conn, st := testService(t)
	ctx := context.Background()

	cj, _ := svc.Create(ctx, CreateRequest{FlavorID: "s1.small", ImageID: "ubuntu-22.04"})
	vmRec := waitVM(t, st, cj.VMID)
	id := vmRec.ID

	// Force stop.
	j, err := svc.Stop(ctx, id)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if done := waitJob(t, st, j.ID); done.State != model.JobSucceeded {
		t.Fatalf("stop job %s", done.State)
	}
	if state, _ := conn.DomainState(ctx, vmRec.DomainName); state != libvirt.StateShutoff {
		t.Errorf("after stop, domain = %v, want shutoff", state)
	}
	got, _ := st.GetVM(ctx, id)
	if got.Status != model.VMStatusStopped {
		t.Errorf("status after stop = %s, want stopped", got.Status)
	}

	// Start again.
	j, _ = svc.Start(ctx, id)
	if done := waitJob(t, st, j.ID); done.State != model.JobSucceeded {
		t.Fatalf("start job %s", done.State)
	}
	if state, _ := conn.DomainState(ctx, vmRec.DomainName); state != libvirt.StateRunning {
		t.Errorf("after start, domain = %v, want running", state)
	}

	// Reboot keeps it running.
	j, _ = svc.Reboot(ctx, id)
	if done := waitJob(t, st, j.ID); done.State != model.JobSucceeded {
		t.Fatalf("reboot job %s", done.State)
	}
}

func TestTerminateTeardown(t *testing.T) {
	svc, conn, st := testService(t)
	ctx := context.Background()

	cj, _ := svc.Create(ctx, CreateRequest{FlavorID: "s1.small", ImageID: "ubuntu-22.04"})
	vmRec := waitVM(t, st, cj.VMID)
	id := vmRec.ID
	seedPath := vmRec.SeedPath

	j, err := svc.Terminate(ctx, id)
	if err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	if done := waitJob(t, st, j.ID); done.State != model.JobSucceeded {
		t.Fatalf("terminate job %s: %s", done.State, done.Error)
	}

	if conn.HasDomain(vmRec.DomainName) {
		t.Error("domain not undefined")
	}
	if conn.HasVolume("default", id+".qcow2") {
		t.Error("overlay volume not deleted")
	}
	if conn.DHCPHostIP("default", vmRec.MAC) != "" {
		t.Error("dhcp lease not released")
	}
	if seedPath != "" {
		if _, err := os.Stat(seedPath); err == nil {
			t.Error("seed iso not removed")
		}
	}
	got, _ := st.GetVM(ctx, id)
	if got.Status != model.VMStatusTerminated {
		t.Errorf("status = %s, want terminated", got.Status)
	}
	// The IP is freed and can be reallocated.
	cj2, _ := svc.Create(ctx, CreateRequest{FlavorID: "s1.small", ImageID: "ubuntu-22.04"})
	vm2 := waitVM(t, st, cj2.VMID)
	if vm2.IP != vmRec.IP {
		t.Errorf("reclaimed IP = %s, want %s", vm2.IP, vmRec.IP)
	}

	// Terminate is idempotent.
	j2, _ := svc.Terminate(ctx, id)
	if done := waitJob(t, st, j2.ID); done.State != model.JobSucceeded {
		t.Errorf("repeat terminate = %s", done.State)
	}

	// Power ops on a terminated VM are rejected.
	if _, err := svc.Start(ctx, id); !errors.Is(err, ErrVMTerminated) {
		t.Errorf("start terminated = %v, want ErrVMTerminated", err)
	}
}

func TestSuspendUnsuspend(t *testing.T) {
	svc, conn, st := testService(t)
	ctx := context.Background()

	cj, _ := svc.Create(ctx, CreateRequest{FlavorID: "s1.small", ImageID: "ubuntu-22.04"})
	rec := waitVM(t, st, cj.VMID)
	id := rec.ID

	j, _ := svc.Suspend(ctx, id)
	if done := waitJob(t, st, j.ID); done.State != model.JobSucceeded {
		t.Fatalf("suspend job %s", done.State)
	}
	got, _ := st.GetVM(ctx, id)
	if got.Status != model.VMStatusSuspended || got.PrevPowerState != model.PowerRunning {
		t.Errorf("after suspend: status=%s prev=%s", got.Status, got.PrevPowerState)
	}
	if state, _ := conn.DomainState(ctx, rec.DomainName); state != libvirt.StateShutoff {
		t.Errorf("suspended domain = %v, want shutoff", state)
	}

	j, _ = svc.Unsuspend(ctx, id)
	if done := waitJob(t, st, j.ID); done.State != model.JobSucceeded {
		t.Fatalf("unsuspend job %s", done.State)
	}
	got, _ = st.GetVM(ctx, id)
	if got.Status != model.VMStatusRunning {
		t.Errorf("after unsuspend status = %s, want running", got.Status)
	}
	if state, _ := conn.DomainState(ctx, rec.DomainName); state != libvirt.StateRunning {
		t.Errorf("unsuspended domain = %v, want running", state)
	}
}

func TestPasswordReset(t *testing.T) {
	svc, _, st := testService(t)
	ctx := context.Background()

	cj, _ := svc.Create(ctx, CreateRequest{FlavorID: "s1.small", ImageID: "ubuntu-22.04", Password: "old"})
	rec := waitVM(t, st, cj.VMID)

	j, err := svc.PasswordReset(ctx, rec.ID, "newpass")
	if err != nil {
		t.Fatalf("PasswordReset: %v", err)
	}
	if done := waitJob(t, st, j.ID); done.State != model.JobSucceeded {
		t.Fatalf("password job %s: %s", done.State, done.Error)
	}
	got, _ := st.GetVM(ctx, rec.ID)
	if got.Password != "newpass" {
		t.Errorf("password not updated: %q", got.Password)
	}

	if _, err := svc.PasswordReset(ctx, rec.ID, ""); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("empty password = %v, want ErrInvalidRequest", err)
	}
}

func TestStats(t *testing.T) {
	svc, conn, st := testService(t)
	ctx := context.Background()

	cj, _ := svc.Create(ctx, CreateRequest{FlavorID: "s1.small", ImageID: "ubuntu-22.04"})
	rec := waitVM(t, st, cj.VMID)

	conn.SetStats(rec.DomainName, libvirt.DomainStats{
		CPUTimeNs:  1_000_000_000,
		NetRxBytes: 1000,
		NetTxBytes: 500,
	})
	// First call establishes the baseline (no rates yet).
	first, err := svc.Stats(ctx, rec.ID)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if first.NetRxBytes != 1000 || first.IntervalSeconds != 0 {
		t.Errorf("first sample = %+v", first)
	}

	// Advance the counters; the next call computes non-negative rates.
	conn.SetStats(rec.DomainName, libvirt.DomainStats{
		CPUTimeNs:  1_500_000_000,
		NetRxBytes: 3000,
		NetTxBytes: 1500,
	})
	time.Sleep(10 * time.Millisecond)
	second, err := svc.Stats(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if second.NetRxBytes != 3000 {
		t.Errorf("raw NetRxBytes = %d, want 3000", second.NetRxBytes)
	}
	if second.IntervalSeconds <= 0 {
		t.Error("expected positive interval on second call")
	}
	if second.NetRxBps <= 0 || second.CPUPercent <= 0 {
		t.Errorf("expected positive rates: rx=%v cpu=%v", second.NetRxBps, second.CPUPercent)
	}

	// Counter reset must not produce a negative rate.
	conn.SetStats(rec.DomainName, libvirt.DomainStats{NetRxBytes: 10})
	time.Sleep(5 * time.Millisecond)
	third, _ := svc.Stats(ctx, rec.ID)
	if third.NetRxBps != 0 {
		t.Errorf("reset should yield 0 rate, got %v", third.NetRxBps)
	}
}

func TestCreateUnknownFlavor(t *testing.T) {
	svc, _, _ := testService(t)
	if _, err := svc.Create(context.Background(), CreateRequest{FlavorID: "nope", ImageID: "ubuntu-22.04"}); !errors.Is(err, ErrFlavorNotFound) {
		t.Errorf("expected ErrFlavorNotFound, got %v", err)
	}
}

func TestCreateUnknownNetwork(t *testing.T) {
	svc, _, _ := testService(t)
	_, err := svc.Create(context.Background(), CreateRequest{
		FlavorID: "s1.small", ImageID: "ubuntu-22.04",
		Network: NetworkRequest{NetworkID: "ghost"},
	})
	if !errors.Is(err, ErrNetworkNotFound) {
		t.Errorf("expected ErrNetworkNotFound, got %v", err)
	}
}
