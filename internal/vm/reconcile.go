package vm

import (
	"context"

	"github.com/kite-plus/kite-kvm/internal/model"
)

// ReconcileOnStart resolves VMs left in a transient 'provisioning' state by a
// crash: their create/rebuild/resize job was interrupted (and already failed by
// the queue's recovery pass), but the VM record never moved on. It is called
// once at startup, after the job queue has recovered.
//
// For each provisioning VM it consults libvirt: if the domain exists, the VM is
// reconciled to the live power state; if the domain is gone, provisioning never
// completed, so the VM is marked error (the billing system can retry) and any
// half-built resources are cleaned up.
func (s *Service) ReconcileOnStart(ctx context.Context) {
	vms, err := s.store.ListVMs(ctx)
	if err != nil {
		s.logger.Error("reconcile: list vms failed", "error", err)
		return
	}
	resolved := 0
	for _, v := range vms {
		if v.Status != model.VMStatusProvisioning {
			continue
		}
		state, err := s.conn.DomainState(ctx, v.DomainName)
		if err != nil {
			// Domain absent: provisioning did not complete. Clean up partials
			// and surface the failure so the create can be retried.
			s.teardownPartial(ctx, v, v.MAC)
			v.Status = model.VMStatusError
			v.PowerState = model.PowerShutoff
			if err := s.store.UpdateVM(ctx, v); err != nil {
				s.logger.Error("reconcile: update vm failed", "vm_id", v.ID, "error", err)
				continue
			}
			s.logger.Warn("reconcile: marked stuck-provisioning vm as error", "vm_id", v.ID)
			resolved++
			continue
		}
		switch mapPowerState(state) {
		case model.PowerRunning:
			v.Status = model.VMStatusRunning
		case model.PowerPaused:
			v.Status = model.VMStatusSuspended
		default:
			v.Status = model.VMStatusStopped
		}
		v.PowerState = mapPowerState(state)
		if err := s.store.UpdateVM(ctx, v); err != nil {
			s.logger.Error("reconcile: update vm failed", "vm_id", v.ID, "error", err)
			continue
		}
		s.logger.Info("reconcile: resolved provisioning vm", "vm_id", v.ID, "status", v.Status)
		resolved++
	}
	if resolved > 0 {
		s.logger.Info("reconcile: resolved interrupted vms", "count", resolved)
	}
}
