package vm

import (
	"context"
	"errors"
	"testing"

	"github.com/kite-plus/kite-kvm/internal/config"
)

func TestHostReport(t *testing.T) {
	svc, _, st := testService(t)
	ctx := context.Background()

	cj, _ := svc.Create(ctx, CreateRequest{FlavorID: "s1.small", ImageID: "ubuntu-22.04"})
	waitVM(t, st, cj.VMID)

	rep, err := svc.HostReport(ctx)
	if err != nil {
		t.Fatalf("HostReport: %v", err)
	}
	if rep.CPUCores != 8 || rep.MemoryTotalMB == 0 {
		t.Errorf("host facts wrong: %+v", rep)
	}
	if rep.VMCount != 1 || rep.CommittedVCPUs != 1 || rep.CommittedMemMB != 1024 {
		t.Errorf("commitments wrong: %+v", rep)
	}
}

func TestAdmissionMaxVMs(t *testing.T) {
	svc, _, st := testService(t)
	ctx := context.Background()
	svc.cfg.Capacity = config.Capacity{MaxVMs: 1}

	cj, _ := svc.Create(ctx, CreateRequest{FlavorID: "s1.small", ImageID: "ubuntu-22.04"})
	waitVM(t, st, cj.VMID)

	_, err := svc.Create(ctx, CreateRequest{FlavorID: "s1.small", ImageID: "ubuntu-22.04"})
	if !errors.Is(err, ErrInsufficientCapacity) {
		t.Fatalf("second create = %v, want ErrInsufficientCapacity", err)
	}
}

func TestAdmissionMemory(t *testing.T) {
	svc, _, st := testService(t)
	ctx := context.Background()
	// Fake host has 16 GiB. Reserve almost all of it so even one small VM
	// (1024 MB) cannot fit.
	svc.cfg.Capacity = config.Capacity{MemOvercommit: 1.0, ReservedMemoryMB: 16000}

	_, err := svc.Create(ctx, CreateRequest{FlavorID: "s1.small", ImageID: "ubuntu-22.04"})
	if !errors.Is(err, ErrInsufficientCapacity) {
		t.Fatalf("create over memory = %v, want ErrInsufficientCapacity", err)
	}

	// With headroom, it is admitted.
	svc.cfg.Capacity = config.Capacity{MemOvercommit: 1.0, ReservedMemoryMB: 0}
	if _, err := svc.Create(ctx, CreateRequest{FlavorID: "s1.small", ImageID: "ubuntu-22.04"}); err != nil {
		t.Fatalf("create within memory = %v, want nil", err)
	}
	_ = st
}
