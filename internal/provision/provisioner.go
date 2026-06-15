package provision

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/kite-plus/kite-kvm/internal/libvirt"
)

// Provisioner builds the bootable artifacts (overlay disk + seed ISO) for a VM.
// It is stateless and idempotent per VM id.
type Provisioner struct {
	conn        libvirt.Conn
	pool        string
	instanceDir string
	buildSeed   func(path string, files []SeedFile) error
}

// NewProvisioner returns a Provisioner that writes overlays into the given
// storage pool and seed ISOs into instanceDir.
func NewProvisioner(conn libvirt.Conn, pool, instanceDir string) *Provisioner {
	return &Provisioner{
		conn:        conn,
		pool:        pool,
		instanceDir: instanceDir,
		buildSeed:   BuildSeedISO,
	}
}

// PrepareRequest is the input to Prepare.
type PrepareRequest struct {
	ID          string
	Hostname    string
	DefaultUser string
	Password    string
	SSHKeys     []string
	BackingPath string
	DiskBytes   uint64
	Network     NetworkConfig
}

// Artifacts are the paths produced by Prepare.
type Artifacts struct {
	DiskPath string
	SeedPath string
}

// Prepare creates the qcow2 overlay and the cloud-init seed ISO for a VM.
func (p *Provisioner) Prepare(ctx context.Context, req PrepareRequest) (Artifacts, error) {
	diskPath, err := CreateOverlay(ctx, p.conn, DiskSpec{
		Pool:        p.pool,
		VolumeName:  req.ID + ".qcow2",
		BackingPath: req.BackingPath,
		SizeBytes:   req.DiskBytes,
	})
	if err != nil {
		return Artifacts{}, fmt.Errorf("create overlay: %w", err)
	}

	ci := CloudInit{
		InstanceID:  req.ID,
		Hostname:    req.Hostname,
		DefaultUser: req.DefaultUser,
		Password:    req.Password,
		SSHKeys:     req.SSHKeys,
		Network:     req.Network,
	}
	seedPath := filepath.Join(p.instanceDir, req.ID+"-seed.iso")
	if err := p.buildSeed(seedPath, ci.Files()); err != nil {
		return Artifacts{}, fmt.Errorf("build seed iso: %w", err)
	}

	return Artifacts{DiskPath: diskPath, SeedPath: seedPath}, nil
}
