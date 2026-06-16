// Package model holds the persistent domain entities shared across the storage,
// service, and API layers. It deliberately depends only on the standard library
// so it can be imported anywhere without creating cycles.
package model

import "time"

// VMStatus is the agent's lifecycle view of a VM (its own truth, distinct from
// the live hypervisor power state).
type VMStatus string

const (
	VMStatusProvisioning VMStatus = "provisioning"
	VMStatusRunning      VMStatus = "running"
	VMStatusStopped      VMStatus = "stopped"
	VMStatusSuspended    VMStatus = "suspended"
	VMStatusError        VMStatus = "error"
	VMStatusTerminated   VMStatus = "terminated"
)

// PowerState mirrors the live libvirt domain state.
type PowerState string

const (
	PowerUnknown PowerState = "unknown"
	PowerRunning PowerState = "running"
	PowerShutoff PowerState = "shutoff"
	PowerPaused  PowerState = "paused"
)

// NetworkMode selects how a VM is attached to the network.
type NetworkMode string

const (
	NetworkNAT    NetworkMode = "nat"
	NetworkBridge NetworkMode = "bridge"
)

// VM is the primary resource: a provisioned virtual machine.
type VM struct {
	ID             string      `json:"id"`
	DomainName     string      `json:"domain_name"`
	DomainUUID     string      `json:"domain_uuid"`
	Hostname       string      `json:"hostname"`
	FlavorID       string      `json:"flavor_id"`
	ImageID        string      `json:"image_id"`
	VCPUs          int         `json:"vcpus"`
	MemoryMB       int         `json:"memory_mb"`
	DiskGB         int         `json:"disk_gb"`
	NetworkID      string      `json:"network_id"`
	NetworkMode    NetworkMode `json:"network_mode"`
	MAC            string      `json:"mac"`
	IP             string      `json:"ip"`
	Gateway        string      `json:"gateway,omitempty"`
	Netmask        string      `json:"netmask,omitempty"`
	Status         VMStatus    `json:"status"`
	PowerState     PowerState  `json:"power_state"`
	PrevPowerState PowerState  `json:"-"`
	DiskPath       string      `json:"-"`
	SeedPath       string      `json:"-"`
	// Password is the initial cloud-init password. Persisted for the async
	// provisioning/password jobs; never serialized to clients.
	Password string   `json:"-"`
	SSHKeys  []string `json:"ssh_keys,omitempty"`

	// Traffic accounting. Quota is the combined in+out byte cap for the current
	// period (0 = unlimited); Used is the accumulated combined transfer.
	TrafficQuotaBytes  uint64    `json:"traffic_quota_bytes"`
	TrafficUsedBytes   uint64    `json:"traffic_used_bytes"`
	TrafficPeriodStart time.Time `json:"traffic_period_start"`
	// NetworkBlocked cuts the VM's NIC link; Reason is "quota" (auto) or
	// "manual" (admin) so the accountant only auto-restores quota blocks.
	NetworkBlocked     bool   `json:"network_blocked"`
	NetworkBlockReason string `json:"network_block_reason,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// JobType identifies the kind of asynchronous operation a Job performs.
type JobType string

const (
	JobCreate         JobType = "create"
	JobStart          JobType = "start"
	JobShutdown       JobType = "shutdown"
	JobReboot         JobType = "reboot"
	JobStop           JobType = "stop"
	JobSuspend        JobType = "suspend"
	JobUnsuspend      JobType = "unsuspend"
	JobPassword       JobType = "password"
	JobHostname       JobType = "hostname"
	JobRebuild        JobType = "rebuild"
	JobResize         JobType = "resize"
	JobSnapshotCreate JobType = "snapshot_create"
	JobSnapshotDelete JobType = "snapshot_delete"
	JobSnapshotRevert JobType = "snapshot_revert"
	JobNetBlock       JobType = "net_block"
	JobNetUnblock     JobType = "net_unblock"
	JobTrafficReset   JobType = "traffic_reset"
	JobTerminate      JobType = "terminate"
)

// JobState is the position of a Job in its state machine.
type JobState string

const (
	JobQueued    JobState = "queued"
	JobRunning   JobState = "running"
	JobSucceeded JobState = "succeeded"
	JobFailed    JobState = "failed"
)

// Job is a unit of asynchronous, mutating work tracked for polling.
type Job struct {
	ID             string            `json:"id"`
	Type           JobType           `json:"type"`
	VMID           string            `json:"vm_id,omitempty"`
	State          JobState          `json:"state"`
	Error          string            `json:"error,omitempty"`
	IdempotencyKey string            `json:"-"`
	Payload        map[string]string `json:"-"` // operation parameters (e.g. snapshot name)
	Attempts       int               `json:"attempts"`
	CreatedAt      time.Time         `json:"created_at"`
	StartedAt      *time.Time        `json:"started_at,omitempty"`
	FinishedAt     *time.Time        `json:"finished_at,omitempty"`
}

// IPAllocation records that an IP within a network is assigned to a VM. The
// (NetworkID, IP) pair is unique, which makes allocation race-safe.
type IPAllocation struct {
	NetworkID string
	IP        string
	VMID      string
	MAC       string
	CreatedAt time.Time
}

// IdempotencyRecord deduplicates retried mutating requests. The key doubles as
// a lock so a retried create never provisions twice; Response holds the stored
// reply to replay within the TTL.
type IdempotencyRecord struct {
	Key         string
	JobID       string
	RequestHash string
	Response    []byte
	StatusCode  int
	CreatedAt   time.Time
	ExpiresAt   time.Time
}
