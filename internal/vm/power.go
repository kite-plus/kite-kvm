package vm

import (
	"context"

	"github.com/kite-plus/kite-kvm/internal/job"
	"github.com/kite-plus/kite-kvm/internal/model"
)

// Start boots a stopped VM.
func (s *Service) Start(ctx context.Context, id string) (*model.Job, error) {
	return s.enqueueOp(ctx, id, model.JobStart)
}

// Shutdown requests a graceful ACPI shutdown.
func (s *Service) Shutdown(ctx context.Context, id string) (*model.Job, error) {
	return s.enqueueOp(ctx, id, model.JobShutdown)
}

// Reboot requests a graceful reboot.
func (s *Service) Reboot(ctx context.Context, id string) (*model.Job, error) {
	return s.enqueueOp(ctx, id, model.JobReboot)
}

// Stop forces the VM off (power cut).
func (s *Service) Stop(ctx context.Context, id string) (*model.Job, error) {
	return s.enqueueOp(ctx, id, model.JobStop)
}

// enqueueOp validates the VM exists and is operable, then schedules a job.
func (s *Service) enqueueOp(ctx context.Context, id string, typ model.JobType) (*model.Job, error) {
	v, err := s.loadOperable(ctx, id)
	if err != nil {
		return nil, err
	}
	j := job.New(typ, v.ID, "")
	if err := s.queue.Enqueue(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

// runPower executes a power operation and records the resulting state. Graceful
// shutdown/reboot complete inside the guest, so the live power state is read
// back (it may still be running immediately after a graceful shutdown; the read
// reconciliation on status endpoints reflects the eventual state).
func (s *Service) runPower(ctx context.Context, vmID string, op model.JobType) error {
	v, err := s.store.GetVM(ctx, vmID)
	if err != nil {
		return err
	}

	var opErr error
	var newStatus model.VMStatus
	switch op {
	case model.JobStart:
		opErr = s.conn.StartDomain(ctx, v.DomainName)
		newStatus = model.VMStatusRunning
	case model.JobShutdown:
		opErr = s.conn.ShutdownDomain(ctx, v.DomainName)
		newStatus = model.VMStatusStopped
	case model.JobReboot:
		opErr = s.conn.RebootDomain(ctx, v.DomainName)
		newStatus = model.VMStatusRunning
	case model.JobStop:
		opErr = s.conn.DestroyDomain(ctx, v.DomainName)
		newStatus = model.VMStatusStopped
	}
	if opErr != nil {
		return opErr
	}

	v.Status = newStatus
	if state, err := s.conn.DomainState(ctx, v.DomainName); err == nil {
		v.PowerState = mapPowerState(state)
	}
	return s.store.UpdateVM(ctx, v)
}
