package vm

import (
	"context"
	"fmt"

	"github.com/kite-plus/kite-kvm/internal/catalog"
	"github.com/kite-plus/kite-kvm/internal/model"
)

// HostReport is the host capacity view: physical resources from libvirt plus the
// resources committed to non-terminated VMs.
type HostReport struct {
	Hostname        string `json:"hostname"`
	LibvirtVersion  string `json:"libvirt_version"`
	CPUCores        int    `json:"cpu_cores"`
	CommittedVCPUs  int    `json:"committed_vcpus"`
	MemoryTotalMB   uint64 `json:"memory_total_mb"`
	MemoryFreeMB    uint64 `json:"memory_free_mb"`
	CommittedMemMB  uint64 `json:"committed_memory_mb"`
	StorageTotalGB  uint64 `json:"storage_total_gb"`
	StorageFreeGB   uint64 `json:"storage_free_gb"`
	CommittedDiskGB uint64 `json:"committed_disk_gb"`
	VMCount         int    `json:"vm_count"`
	RunningCount    int    `json:"running_count"`
}

// commitment is the resources committed across non-terminated VMs.
type commitment struct {
	vms     int
	running int
	vcpus   int
	memMB   uint64
	diskGB  uint64
}

func (s *Service) committed(ctx context.Context) (commitment, error) {
	vms, err := s.store.ListVMs(ctx)
	if err != nil {
		return commitment{}, err
	}
	var c commitment
	for _, v := range vms {
		if v.Status == model.VMStatusTerminated {
			continue
		}
		c.vms++
		c.vcpus += v.VCPUs
		c.memMB += uint64(v.MemoryMB)
		c.diskGB += uint64(v.DiskGB)
		if v.Status == model.VMStatusRunning {
			c.running++
		}
	}
	return c, nil
}

// HostReport returns the host's capacity and current commitments.
func (s *Service) HostReport(ctx context.Context) (*HostReport, error) {
	hi, err := s.conn.HostInfo(ctx, s.cfg.Libvirt.StoragePool)
	if err != nil {
		return nil, err
	}
	c, err := s.committed(ctx)
	if err != nil {
		return nil, err
	}
	return &HostReport{
		Hostname:        hi.Hostname,
		LibvirtVersion:  hi.LibvirtVersion,
		CPUCores:        hi.CPUs,
		CommittedVCPUs:  c.vcpus,
		MemoryTotalMB:   hi.MemoryTotalBytes >> 20,
		MemoryFreeMB:    hi.MemoryFreeBytes >> 20,
		CommittedMemMB:  c.memMB,
		StorageTotalGB:  hi.StorageBytes >> 30,
		StorageFreeGB:   hi.StorageFreeBytes >> 30,
		CommittedDiskGB: c.diskGB,
		VMCount:         c.vms,
		RunningCount:    c.running,
	}, nil
}

// admit enforces the configured capacity limits before accepting a create. A
// zero-valued limit is unenforced; if host info is unavailable the resource
// checks are skipped (the VM-count cap still applies).
func (s *Service) admit(ctx context.Context, flavor catalog.Flavor) error {
	cap := s.cfg.Capacity
	c, err := s.committed(ctx)
	if err != nil {
		return err
	}
	if cap.MaxVMs > 0 && c.vms >= cap.MaxVMs {
		return fmt.Errorf("%w: host VM limit (%d) reached", ErrInsufficientCapacity, cap.MaxVMs)
	}
	if cap.MemOvercommit <= 0 && cap.CPUOvercommit <= 0 {
		return nil
	}
	hi, err := s.conn.HostInfo(ctx, s.cfg.Libvirt.StoragePool)
	if err != nil {
		s.logger.Warn("admission: host info unavailable, skipping resource checks", "error", err)
		return nil
	}
	if cap.MemOvercommit > 0 {
		totalMB := float64(hi.MemoryTotalBytes >> 20)
		allowed := (totalMB - float64(cap.ReservedMemoryMB)) * cap.MemOvercommit
		if float64(c.memMB)+float64(flavor.MemoryMB) > allowed {
			return fmt.Errorf("%w: not enough memory (committed %d MB + %d MB > %0.f MB allowed)",
				ErrInsufficientCapacity, c.memMB, flavor.MemoryMB, allowed)
		}
	}
	if cap.CPUOvercommit > 0 {
		allowed := float64(hi.CPUs) * cap.CPUOvercommit
		if float64(c.vcpus)+float64(flavor.VCPUs) > allowed {
			return fmt.Errorf("%w: not enough vCPU (committed %d + %d > %0.f allowed)",
				ErrInsufficientCapacity, c.vcpus, flavor.VCPUs, allowed)
		}
	}
	return nil
}
