package vm

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/kite-plus/kite-kvm/internal/job"
	"github.com/kite-plus/kite-kvm/internal/libvirt"
	"github.com/kite-plus/kite-kvm/internal/model"
)

// SnapshotList returns the VM's snapshots.
func (s *Service) SnapshotList(ctx context.Context, id string) ([]libvirt.SnapshotInfo, error) {
	v, err := s.loadOperable(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.conn.ListSnapshots(ctx, v.DomainName)
}

// SnapshotCreate schedules creation of a snapshot. A blank name is auto-generated.
func (s *Service) SnapshotCreate(ctx context.Context, id, name, description string) (*model.Job, error) {
	v, err := s.loadOperable(ctx, id)
	if err != nil {
		return nil, err
	}
	name = sanitizeName(name)
	if name == "" {
		name = "snap-" + uuid.NewString()[:8]
	}
	return s.enqueueSnapshotJob(ctx, v.ID, model.JobSnapshotCreate, map[string]string{
		"name":        name,
		"description": description,
	})
}

// SnapshotDelete schedules deletion of a snapshot.
func (s *Service) SnapshotDelete(ctx context.Context, id, name string) (*model.Job, error) {
	v, err := s.loadOperable(ctx, id)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, fmt.Errorf("%w: snapshot name is required", ErrInvalidRequest)
	}
	return s.enqueueSnapshotJob(ctx, v.ID, model.JobSnapshotDelete, map[string]string{"name": name})
}

// SnapshotRevert schedules a revert to a snapshot.
func (s *Service) SnapshotRevert(ctx context.Context, id, name string) (*model.Job, error) {
	v, err := s.loadOperable(ctx, id)
	if err != nil {
		return nil, err
	}
	if name == "" {
		return nil, fmt.Errorf("%w: snapshot name is required", ErrInvalidRequest)
	}
	return s.enqueueSnapshotJob(ctx, v.ID, model.JobSnapshotRevert, map[string]string{"name": name})
}

func (s *Service) enqueueSnapshotJob(ctx context.Context, vmID string, typ model.JobType, payload map[string]string) (*model.Job, error) {
	j := job.New(typ, vmID, "")
	j.Payload = payload
	if err := s.queue.Enqueue(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

func (s *Service) runSnapshotCreate(ctx context.Context, j *model.Job) error {
	v, err := s.store.GetVM(ctx, j.VMID)
	if err != nil {
		return err
	}
	return s.conn.CreateSnapshot(ctx, v.DomainName, j.Payload["name"], j.Payload["description"])
}

func (s *Service) runSnapshotDelete(ctx context.Context, j *model.Job) error {
	v, err := s.store.GetVM(ctx, j.VMID)
	if err != nil {
		return err
	}
	return s.conn.DeleteSnapshot(ctx, v.DomainName, j.Payload["name"])
}

func (s *Service) runSnapshotRevert(ctx context.Context, j *model.Job) error {
	v, err := s.store.GetVM(ctx, j.VMID)
	if err != nil {
		return err
	}
	if err := s.conn.RevertSnapshot(ctx, v.DomainName, j.Payload["name"]); err != nil {
		return err
	}
	// Reverting may change the power state; reconcile and persist.
	if state, err := s.conn.DomainState(ctx, v.DomainName); err == nil {
		v.PowerState = mapPowerState(state)
		switch v.PowerState {
		case model.PowerRunning:
			v.Status = model.VMStatusRunning
		case model.PowerShutoff:
			v.Status = model.VMStatusStopped
		}
		return s.store.UpdateVM(ctx, v)
	}
	return nil
}

// sanitizeName reduces a string to snapshot-safe characters.
func sanitizeName(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			out = append(out, r)
		}
	}
	return string(out)
}
