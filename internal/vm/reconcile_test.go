package vm

import (
	"context"
	"testing"

	"github.com/kite-plus/kite-kvm/internal/model"
)

func TestReconcileOnStartResolvesRunning(t *testing.T) {
	svc, _, st := testService(t)
	ctx := context.Background()

	// A VM whose create finished (domain running) but was left 'provisioning'
	// because the agent crashed before persisting the final status.
	cj, _ := svc.Create(ctx, CreateRequest{FlavorID: "s1.small", ImageID: "ubuntu-22.04"})
	rec := waitVM(t, st, cj.VMID)
	rec.Status = model.VMStatusProvisioning
	if err := st.UpdateVM(ctx, rec); err != nil {
		t.Fatal(err)
	}

	svc.ReconcileOnStart(ctx)

	got, _ := st.GetVM(ctx, rec.ID)
	if got.Status != model.VMStatusRunning {
		t.Errorf("status = %s, want running", got.Status)
	}
}

func TestReconcileOnStartErrorsOrphan(t *testing.T) {
	svc, conn, st := testService(t)
	ctx := context.Background()

	cj, _ := svc.Create(ctx, CreateRequest{FlavorID: "s1.small", ImageID: "ubuntu-22.04"})
	rec := waitVM(t, st, cj.VMID)
	// Simulate a crash mid-create: VM stuck provisioning, domain never defined.
	_ = conn.DestroyDomain(ctx, rec.DomainName)
	_ = conn.UndefineDomain(ctx, rec.DomainName)
	rec.Status = model.VMStatusProvisioning
	if err := st.UpdateVM(ctx, rec); err != nil {
		t.Fatal(err)
	}

	svc.ReconcileOnStart(ctx)

	got, _ := st.GetVM(ctx, rec.ID)
	if got.Status != model.VMStatusError {
		t.Errorf("status = %s, want error (orphaned provisioning)", got.Status)
	}
}
