package vm

import (
	"context"
	"testing"

	"github.com/kite-plus/kite-kvm/internal/libvirt"
	"github.com/kite-plus/kite-kvm/internal/model"
)

func TestTrafficAccountingAndCutoff(t *testing.T) {
	svc, conn, st := testService(t)
	ctx := context.Background()

	// 10 GiB quota so the test math is small but exercises the uint64 path.
	cj, _ := svc.Create(ctx, CreateRequest{FlavorID: "s1.small", ImageID: "ubuntu-22.04"})
	rec := waitVM(t, st, cj.VMID)
	if _, err := svc.SetTrafficQuota(ctx, rec.ID, 10); err != nil {
		t.Fatalf("SetTrafficQuota: %v", err)
	}

	dom := rec.DomainName
	half := uint64(5) * gib

	// First tick seeds the baseline (no usage accrues yet).
	conn.SetStats(dom, libvirt.DomainStats{Name: dom, NetRxBytes: 1000, NetTxBytes: 2000})
	svc.accountTick(ctx)
	if got, _ := st.GetVM(ctx, rec.ID); got.TrafficUsedBytes != 0 {
		t.Fatalf("first tick should only seed, used=%d", got.TrafficUsedBytes)
	}

	// Accrue ~half the quota (rx+tx combined), still under cap.
	conn.SetStats(dom, libvirt.DomainStats{Name: dom, NetRxBytes: 1000 + half/2, NetTxBytes: 2000 + half/2})
	svc.accountTick(ctx)
	got, _ := st.GetVM(ctx, rec.ID)
	if got.TrafficUsedBytes != half {
		t.Fatalf("used = %d, want %d", got.TrafficUsedBytes, half)
	}
	if got.NetworkBlocked {
		t.Fatal("should not be blocked under quota")
	}
	if conn.IsLinkDown(dom) {
		t.Fatal("link should be up under quota")
	}

	// Cross the quota -> auto cutoff (link down).
	conn.SetStats(dom, libvirt.DomainStats{Name: dom, NetRxBytes: 1000 + 11*gib, NetTxBytes: 2000 + 11*gib})
	svc.accountTick(ctx)
	got, _ = st.GetVM(ctx, rec.ID)
	if !got.NetworkBlocked || got.NetworkBlockReason != blockReasonQuota {
		t.Fatalf("expected quota block, got blocked=%v reason=%q", got.NetworkBlocked, got.NetworkBlockReason)
	}
	if !conn.IsLinkDown(dom) {
		t.Fatal("NIC link should be down after quota cutoff")
	}

	// Reset restores network and zeroes usage.
	jr, _ := svc.ResetTraffic(ctx, rec.ID)
	if done := waitJob(t, st, jr.ID); done.State != model.JobSucceeded {
		t.Fatalf("reset job %s", done.State)
	}
	got, _ = st.GetVM(ctx, rec.ID)
	if got.NetworkBlocked || got.TrafficUsedBytes != 0 {
		t.Fatalf("after reset: blocked=%v used=%d", got.NetworkBlocked, got.TrafficUsedBytes)
	}
	if conn.IsLinkDown(dom) {
		t.Fatal("link should be restored after reset")
	}
}

func TestManualBlockNotAutoRestored(t *testing.T) {
	svc, conn, st := testService(t)
	ctx := context.Background()

	cj, _ := svc.Create(ctx, CreateRequest{FlavorID: "s1.small", ImageID: "ubuntu-22.04"})
	rec := waitVM(t, st, cj.VMID)

	jb, _ := svc.BlockNetwork(ctx, rec.ID)
	if done := waitJob(t, st, jb.ID); done.State != model.JobSucceeded {
		t.Fatalf("block job %s", done.State)
	}
	if !conn.IsLinkDown(rec.DomainName) {
		t.Fatal("manual block should cut the link")
	}

	// An accounting tick (well under any quota) must NOT lift a manual block.
	svc.accountTick(ctx)
	got, _ := st.GetVM(ctx, rec.ID)
	if !got.NetworkBlocked || got.NetworkBlockReason != blockReasonManual {
		t.Fatalf("manual block should survive accounting: blocked=%v reason=%q", got.NetworkBlocked, got.NetworkBlockReason)
	}

	ju, _ := svc.UnblockNetwork(ctx, rec.ID)
	waitJob(t, st, ju.ID)
	if conn.IsLinkDown(rec.DomainName) {
		t.Fatal("unblock should restore the link")
	}
}
