// Package libvirt is the single seam over the underlying libvirt binding. Every
// other package depends only on the Conn interface, never on go-libvirt
// directly, so all business logic compiles and is unit-testable on macOS via
// the in-memory Fake.
package libvirt

import (
	"context"
	"errors"
	"time"
)

// DomainState is the libvirt power state, decoupled from the binding's enums
// and from the agent's own model.PowerState.
type DomainState int

const (
	StateUnknown DomainState = iota
	StateRunning
	StateShutoff
	StatePaused
)

func (s DomainState) String() string {
	switch s {
	case StateRunning:
		return "running"
	case StateShutoff:
		return "shutoff"
	case StatePaused:
		return "paused"
	default:
		return "unknown"
	}
}

// ErrDomainNotFound is returned when an operation targets a missing domain.
var ErrDomainNotFound = errors.New("libvirt: domain not found")

// DomainStats is the parsed, billing-relevant slice of the bulk domain stats.
type DomainStats struct {
	Name            string
	State           DomainState
	CPUTimeNs       uint64
	MemBalloonKiB   uint64
	MemRSSKiB       uint64
	NetRxBytes      uint64
	NetTxBytes      uint64
	BlockRdBytes    uint64
	BlockWrBytes    uint64
	BlockAllocation uint64
	BlockCapacity   uint64
}

// SnapshotInfo describes a domain snapshot.
type SnapshotInfo struct {
	Name         string    `json:"name"`
	State        string    `json:"state,omitempty"`
	CreationTime time.Time `json:"creation_time"`
	Current      bool      `json:"current"`
}

// StorageVolSpec describes a storage volume to create. A non-empty BackingPath
// creates a thin copy-on-write overlay over a read-only base image.
type StorageVolSpec struct {
	Pool          string
	Name          string
	CapacityBytes uint64
	BackingPath   string // golden base image; "" => standalone volume
	BackingFmt    string // e.g. "qcow2"
}

// Conn is the single seam over the libvirt binding. The real implementation
// wraps the go-libvirt RPC client; Fake provides an in-memory implementation.
type Conn interface {
	// Connection.
	Connect(ctx context.Context) error
	Close() error
	Ping(ctx context.Context) error // backs /readyz

	// Domain lifecycle (define persistent config, then start).
	DefineDomain(ctx context.Context, xml string) (uuid string, err error)
	StartDomain(ctx context.Context, name string) error
	ShutdownDomain(ctx context.Context, name string) error // graceful ACPI
	RebootDomain(ctx context.Context, name string) error
	DestroyDomain(ctx context.Context, name string) error // forced power-off
	SuspendDomain(ctx context.Context, name string) error // pause vCPUs
	ResumeDomain(ctx context.Context, name string) error
	UndefineDomain(ctx context.Context, name string) error

	// Read / state.
	DomainState(ctx context.Context, name string) (DomainState, error)
	ListDomains(ctx context.Context) ([]string, error)
	DomainXML(ctx context.Context, name string) (string, error)
	// DomainVNCAddress returns the live VNC listen host and TCP port of a
	// running domain (used to proxy a browser console).
	DomainVNCAddress(ctx context.Context, name string) (host string, port int, err error)

	// Storage.
	CreateVolume(ctx context.Context, spec StorageVolSpec) (path string, err error)
	DeleteVolume(ctx context.Context, pool, name string) error
	ResizeVolume(ctx context.Context, pool, name string, capacityBytes uint64) error

	// Bulk stats (for billing / info).
	AllDomainStats(ctx context.Context) ([]DomainStats, error)

	// Network: NAT static DHCP lease (MAC -> IP) on a libvirt network.
	AddDHCPHost(ctx context.Context, network, mac, name, ip string) error
	RemoveDHCPHost(ctx context.Context, network, mac string) error

	// Snapshots.
	CreateSnapshot(ctx context.Context, domain, name, description string) error
	ListSnapshots(ctx context.Context, domain string) ([]SnapshotInfo, error)
	DeleteSnapshot(ctx context.Context, domain, name string) error
	RevertSnapshot(ctx context.Context, domain, name string) error
}
