package vm

import (
	"context"
	"fmt"

	"github.com/kite-plus/kite-kvm/internal/domainxml"
	"github.com/kite-plus/kite-kvm/internal/job"
	"github.com/kite-plus/kite-kvm/internal/model"
	"github.com/kite-plus/kite-kvm/internal/provision"
)

// RebuildRequest is the body of POST /v1/vms/{id}/rebuild. Empty fields keep the
// VM's current values.
type RebuildRequest struct {
	ImageID  string   `json:"image_id,omitempty"`
	Password string   `json:"password,omitempty"`
	SSHKeys  []string `json:"ssh_keys,omitempty"`
}

// Rebuild reinstalls the VM from an image. The root disk is recreated and all
// data on it is lost. Optional fields override the image, password, and SSH
// keys; otherwise the current values are reused.
func (s *Service) Rebuild(ctx context.Context, id string, req RebuildRequest) (*model.Job, error) {
	v, err := s.loadOperable(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.ImageID != "" {
		if _, ok := s.catalog.Image(req.ImageID); !ok {
			return nil, fmt.Errorf("%w: %s", ErrImageNotFound, req.ImageID)
		}
		v.ImageID = req.ImageID
	}
	if req.Password != "" {
		v.Password = req.Password
	}
	if req.SSHKeys != nil {
		v.SSHKeys = req.SSHKeys
	}
	v.Status = model.VMStatusProvisioning
	if err := s.store.UpdateVM(ctx, v); err != nil {
		return nil, err
	}

	j := job.New(model.JobRebuild, id, "")
	if err := s.queue.Enqueue(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

func (s *Service) runRebuild(ctx context.Context, vmID string) error {
	v, err := s.store.GetVM(ctx, vmID)
	if err != nil {
		return err
	}
	image, ok := s.catalog.Image(v.ImageID)
	if !ok {
		return s.failVM(ctx, v, fmt.Errorf("image %q no longer configured", v.ImageID))
	}

	// Stop and discard the old root disk.
	_ = s.conn.DestroyDomain(ctx, v.DomainName)
	if err := s.conn.DeleteVolume(ctx, s.cfg.Libvirt.StoragePool, v.ID+".qcow2"); err != nil {
		return s.failVM(ctx, v, fmt.Errorf("delete old disk: %w", err))
	}

	// Recreate the overlay and seed from the (possibly new) image.
	art, err := s.provisioner.Prepare(ctx, provision.PrepareRequest{
		ID:          v.ID,
		Hostname:    v.Hostname,
		DefaultUser: image.DefaultUser,
		Password:    v.Password,
		SSHKeys:     v.SSHKeys,
		BackingPath: image.BasePath,
		DiskBytes:   uint64(v.DiskGB) * gib,
		Network:     s.networkConfigFor(v),
	})
	if err != nil {
		return s.failVM(ctx, v, fmt.Errorf("rebuild provision: %w", err))
	}
	v.DiskPath = art.DiskPath
	v.SeedPath = art.SeedPath

	// Redefine (XML is image-agnostic, but the seed/disk were recreated) and start.
	xml, err := domainxml.Render(s.buildDomainSpec(v))
	if err != nil {
		return s.failVM(ctx, v, fmt.Errorf("render domain xml: %w", err))
	}
	if _, err := s.conn.DefineDomain(ctx, xml); err != nil {
		return s.failVM(ctx, v, fmt.Errorf("define domain: %w", err))
	}
	if err := s.conn.StartDomain(ctx, v.DomainName); err != nil {
		return s.failVM(ctx, v, fmt.Errorf("start domain: %w", err))
	}

	v.Status = model.VMStatusRunning
	v.PowerState = model.PowerRunning
	s.logger.Info("vm rebuilt", "vm_id", v.ID, "image", v.ImageID)
	return s.store.UpdateVM(ctx, v)
}
