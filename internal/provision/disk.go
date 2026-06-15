package provision

import (
	"context"

	"github.com/kite-plus/kite-kvm/internal/libvirt"
)

// DiskSpec describes the per-VM root overlay to create.
type DiskSpec struct {
	Pool        string
	VolumeName  string // e.g. "<id>.qcow2"
	BackingPath string // golden base image (read-only)
	SizeBytes   uint64 // virtual capacity (== flavor disk size)
}

// CreateOverlay creates a thin qcow2 copy-on-write overlay over the golden base
// image via the libvirt storage volume API and returns its path.
func CreateOverlay(ctx context.Context, conn libvirt.Conn, spec DiskSpec) (string, error) {
	return conn.CreateVolume(ctx, libvirt.StorageVolSpec{
		Pool:          spec.Pool,
		Name:          spec.VolumeName,
		CapacityBytes: spec.SizeBytes,
		BackingPath:   spec.BackingPath,
		BackingFmt:    "qcow2",
	})
}
