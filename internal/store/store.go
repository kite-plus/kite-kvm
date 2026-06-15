// Package store provides durable persistence for the agent: the VM↔domain
// mapping, jobs, idempotency records, and IP allocations. The Store interface
// decouples callers from the SQLite implementation.
package store

import (
	"context"
	"errors"

	"github.com/kite-plus/kite-kvm/internal/model"
)

// Sentinel errors returned by Store implementations.
var (
	ErrNotFound      = errors.New("store: not found")
	ErrConflict      = errors.New("store: conflict")
	ErrNoIPAvailable = errors.New("store: no ip available in network")
)

// Store is the durable persistence interface.
type Store interface {
	// Close releases the underlying resources.
	Close() error

	// VMs.
	CreateVM(ctx context.Context, vm *model.VM) error
	GetVM(ctx context.Context, id string) (*model.VM, error)
	ListVMs(ctx context.Context) ([]*model.VM, error)
	UpdateVM(ctx context.Context, vm *model.VM) error
	DeleteVM(ctx context.Context, id string) error

	// Jobs.
	CreateJob(ctx context.Context, job *model.Job) error
	GetJob(ctx context.Context, id string) (*model.Job, error)
	UpdateJob(ctx context.Context, job *model.Job) error
	ListJobsByState(ctx context.Context, state model.JobState) ([]*model.Job, error)

	// Idempotency.
	GetIdempotency(ctx context.Context, key string) (*model.IdempotencyRecord, error)
	PutIdempotency(ctx context.Context, rec *model.IdempotencyRecord) error
	UpdateIdempotency(ctx context.Context, rec *model.IdempotencyRecord) error

	// IP allocations. AllocateIP atomically claims the first candidate not
	// already allocated within the network, making concurrent provisioning
	// race-safe. ReleaseIPByVM frees every allocation held by a VM.
	AllocateIP(ctx context.Context, networkID, vmID, mac string, candidates []string) (string, error)
	ReleaseIPByVM(ctx context.Context, vmID string) error
	AllocatedIPs(ctx context.Context, networkID string) (map[string]struct{}, error)
}
