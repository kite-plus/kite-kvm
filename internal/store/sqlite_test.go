package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kite-plus/kite-kvm/internal/model"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	st, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func sampleVM(id string) *model.VM {
	return &model.VM{
		ID:          id,
		DomainName:  "kvm-" + id,
		Hostname:    "host-" + id,
		FlavorID:    "s1.small",
		ImageID:     "ubuntu-22.04",
		VCPUs:       1,
		MemoryMB:    1024,
		DiskGB:      20,
		NetworkID:   "nat-default",
		NetworkMode: model.NetworkNAT,
		Status:      model.VMStatusProvisioning,
		PowerState:  model.PowerShutoff,
		SSHKeys:     []string{"ssh-ed25519 AAAA test"},
	}
}

func TestVMCRUD(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	vm := sampleVM("vm1")
	if err := st.CreateVM(ctx, vm); err != nil {
		t.Fatalf("CreateVM: %v", err)
	}
	if vm.CreatedAt.IsZero() || vm.UpdatedAt.IsZero() {
		t.Error("timestamps not stamped on create")
	}

	got, err := st.GetVM(ctx, "vm1")
	if err != nil {
		t.Fatalf("GetVM: %v", err)
	}
	if got.Hostname != "host-vm1" || got.NetworkMode != model.NetworkNAT {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if len(got.SSHKeys) != 1 || got.SSHKeys[0] != "ssh-ed25519 AAAA test" {
		t.Errorf("ssh keys not round-tripped: %v", got.SSHKeys)
	}

	got.Status = model.VMStatusRunning
	got.PowerState = model.PowerRunning
	got.IP = "192.168.122.50"
	if err := st.UpdateVM(ctx, got); err != nil {
		t.Fatalf("UpdateVM: %v", err)
	}
	reread, _ := st.GetVM(ctx, "vm1")
	if reread.Status != model.VMStatusRunning || reread.IP != "192.168.122.50" {
		t.Errorf("update not persisted: %+v", reread)
	}

	list, err := st.ListVMs(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListVMs: %v len=%d", err, len(list))
	}

	if err := st.DeleteVM(ctx, "vm1"); err != nil {
		t.Fatalf("DeleteVM: %v", err)
	}
	if _, err := st.GetVM(ctx, "vm1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestVMTrafficRoundTrip(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	vm := sampleVM("tvm")
	vm.TrafficQuotaBytes = 2 * 1024 * 1024 * 1024 * 1024 // 2 TB
	vm.TrafficUsedBytes = 1500 * 1024 * 1024 * 1024      // 1.5 TB
	vm.NetworkBlocked = false
	if err := st.CreateVM(ctx, vm); err != nil {
		t.Fatalf("CreateVM: %v", err)
	}

	got, _ := st.GetVM(ctx, "tvm")
	if got.TrafficQuotaBytes != vm.TrafficQuotaBytes || got.TrafficUsedBytes != vm.TrafficUsedBytes {
		t.Errorf("traffic not round-tripped: quota=%d used=%d", got.TrafficQuotaBytes, got.TrafficUsedBytes)
	}

	got.TrafficUsedBytes = got.TrafficQuotaBytes
	got.NetworkBlocked = true
	got.NetworkBlockReason = "quota"
	if err := st.UpdateVM(ctx, got); err != nil {
		t.Fatalf("UpdateVM: %v", err)
	}
	reread, _ := st.GetVM(ctx, "tvm")
	if !reread.NetworkBlocked || reread.NetworkBlockReason != "quota" {
		t.Errorf("block state not persisted: %+v", reread)
	}
	if reread.TrafficUsedBytes != vm.TrafficQuotaBytes {
		t.Errorf("used not persisted: %d", reread.TrafficUsedBytes)
	}
}

func TestVMDuplicateConflict(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	if err := st.CreateVM(ctx, sampleVM("dup")); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateVM(ctx, sampleVM("dup")); !errors.Is(err, ErrConflict) {
		t.Errorf("expected ErrConflict on duplicate id, got %v", err)
	}
}

func TestJobLifecycle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	job := &model.Job{ID: "job1", Type: model.JobCreate, VMID: "vm1", State: model.JobQueued}
	if err := st.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	now := time.Now().UTC()
	job.State = model.JobRunning
	job.StartedAt = &now
	if err := st.UpdateJob(ctx, job); err != nil {
		t.Fatalf("UpdateJob: %v", err)
	}
	queued, err := st.ListJobsByState(ctx, model.JobQueued)
	if err != nil {
		t.Fatal(err)
	}
	if len(queued) != 0 {
		t.Errorf("expected no queued jobs, got %d", len(queued))
	}
	got, _ := st.GetJob(ctx, "job1")
	if got.State != model.JobRunning || got.StartedAt == nil {
		t.Errorf("job update not persisted: %+v", got)
	}
}

func TestIdempotency(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	rec := &model.IdempotencyRecord{
		Key:         "k1",
		RequestHash: "abc",
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	if err := st.PutIdempotency(ctx, rec); err != nil {
		t.Fatalf("PutIdempotency: %v", err)
	}
	if err := st.PutIdempotency(ctx, rec); !errors.Is(err, ErrConflict) {
		t.Errorf("expected conflict on duplicate key, got %v", err)
	}
	rec.JobID = "job1"
	rec.Response = []byte(`{"ok":true}`)
	rec.StatusCode = 202
	if err := st.UpdateIdempotency(ctx, rec); err != nil {
		t.Fatalf("UpdateIdempotency: %v", err)
	}
	got, err := st.GetIdempotency(ctx, "k1")
	if err != nil {
		t.Fatal(err)
	}
	if got.JobID != "job1" || got.StatusCode != 202 || string(got.Response) != `{"ok":true}` {
		t.Errorf("idempotency not updated: %+v", got)
	}
}

func TestIPAllocation(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	candidates := []string{"203.0.113.10", "203.0.113.11", "203.0.113.12"}

	ip1, err := st.AllocateIP(ctx, "public-1", "vmA", "52:54:00:aa:bb:01", candidates)
	if err != nil {
		t.Fatalf("AllocateIP: %v", err)
	}
	if ip1 != "203.0.113.10" {
		t.Errorf("first alloc = %s, want .10", ip1)
	}
	ip2, err := st.AllocateIP(ctx, "public-1", "vmB", "52:54:00:aa:bb:02", candidates)
	if err != nil {
		t.Fatalf("AllocateIP 2: %v", err)
	}
	if ip2 != "203.0.113.11" {
		t.Errorf("second alloc = %s, want .11", ip2)
	}

	allocated, _ := st.AllocatedIPs(ctx, "public-1")
	if len(allocated) != 2 {
		t.Errorf("allocated count = %d, want 2", len(allocated))
	}

	// Release vmA's IP, then a new VM should reclaim .10.
	if err := st.ReleaseIPByVM(ctx, "vmA"); err != nil {
		t.Fatalf("ReleaseIPByVM: %v", err)
	}
	ip3, err := st.AllocateIP(ctx, "public-1", "vmC", "52:54:00:aa:bb:03", candidates)
	if err != nil {
		t.Fatalf("AllocateIP 3: %v", err)
	}
	if ip3 != "203.0.113.10" {
		t.Errorf("reclaimed alloc = %s, want .10", ip3)
	}

	// All offered candidates are taken (.10 by vmC, .11 by vmB) -> no IP available.
	if _, err := st.AllocateIP(ctx, "public-1", "vmD", "", []string{"203.0.113.10", "203.0.113.11"}); !errors.Is(err, ErrNoIPAvailable) {
		t.Errorf("expected ErrNoIPAvailable, got %v", err)
	}
}
