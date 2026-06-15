// Package job runs mutating VM operations asynchronously through an in-process
// worker pool, persisting each job's state machine so polling survives restarts.
package job

import (
	"github.com/google/uuid"

	"github.com/kite-plus/kite-kvm/internal/model"
)

// New creates a queued Job with a fresh id.
func New(typ model.JobType, vmID, idempotencyKey string) *model.Job {
	return &model.Job{
		ID:             uuid.NewString(),
		Type:           typ,
		VMID:           vmID,
		State:          model.JobQueued,
		IdempotencyKey: idempotencyKey,
	}
}
