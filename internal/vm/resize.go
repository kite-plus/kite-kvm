package vm

import (
	"context"
	"fmt"

	"github.com/kite-plus/kite-kvm/internal/domainxml"
	"github.com/kite-plus/kite-kvm/internal/job"
	"github.com/kite-plus/kite-kvm/internal/model"
)

// ResizeRequest is the body of POST /v1/vms/{id}/resize (change package).
type ResizeRequest struct {
	FlavorID string `json:"flavor_id"`
}

// Resize changes the VM's flavor. The disk is grow-only (a smaller target disk
// is rejected). The change is applied by a brief power-cycle.
func (s *Service) Resize(ctx context.Context, id string, req ResizeRequest) (*model.Job, error) {
	v, err := s.loadOperable(ctx, id)
	if err != nil {
		return nil, err
	}
	flavor, ok := s.catalog.Flavor(req.FlavorID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrFlavorNotFound, req.FlavorID)
	}
	if flavor.DiskGB < v.DiskGB {
		return nil, fmt.Errorf("%w: disk cannot shrink (current %dGB, target %dGB)", ErrInvalidRequest, v.DiskGB, flavor.DiskGB)
	}

	v.FlavorID = flavor.ID
	v.VCPUs = flavor.VCPUs
	v.MemoryMB = flavor.MemoryMB
	v.DiskGB = flavor.DiskGB
	v.Status = model.VMStatusProvisioning
	if err := s.store.UpdateVM(ctx, v); err != nil {
		return nil, err
	}

	j := job.New(model.JobResize, id, "")
	if err := s.queue.Enqueue(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

func (s *Service) runResize(ctx context.Context, vmID string) error {
	v, err := s.store.GetVM(ctx, vmID)
	if err != nil {
		return err
	}

	wasRunning := false
	if state, err := s.conn.DomainState(ctx, v.DomainName); err == nil {
		wasRunning = mapPowerState(state) == model.PowerRunning
	}

	// New vCPU/memory apply cleanly while off; stop first.
	if wasRunning {
		if err := s.conn.DestroyDomain(ctx, v.DomainName); err != nil {
			return s.failVM(ctx, v, fmt.Errorf("stop for resize: %w", err))
		}
	}

	// Grow the root disk (grow-only validated at request time).
	if err := s.conn.ResizeVolume(ctx, s.cfg.Libvirt.StoragePool, v.ID+".qcow2", uint64(v.DiskGB)*gib); err != nil {
		return s.failVM(ctx, v, fmt.Errorf("resize disk: %w", err))
	}
	// Re-seed so cloud-init growpart expands the filesystem on next boot.
	if err := s.reseedVM(ctx, v); err != nil {
		return s.failVM(ctx, v, err)
	}

	// Redefine the domain with the new vCPU/memory.
	xml, err := domainxml.Render(s.buildDomainSpec(v))
	if err != nil {
		return s.failVM(ctx, v, fmt.Errorf("render domain xml: %w", err))
	}
	if _, err := s.conn.DefineDomain(ctx, xml); err != nil {
		return s.failVM(ctx, v, fmt.Errorf("define domain: %w", err))
	}

	if wasRunning {
		if err := s.conn.StartDomain(ctx, v.DomainName); err != nil {
			return s.failVM(ctx, v, fmt.Errorf("start domain: %w", err))
		}
		v.Status = model.VMStatusRunning
		v.PowerState = model.PowerRunning
	} else {
		v.Status = model.VMStatusStopped
		v.PowerState = model.PowerShutoff
	}
	s.logger.Info("vm resized", "vm_id", v.ID, "flavor", v.FlavorID, "vcpus", v.VCPUs, "memory_mb", v.MemoryMB, "disk_gb", v.DiskGB)
	return s.store.UpdateVM(ctx, v)
}
