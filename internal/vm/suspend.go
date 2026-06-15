package vm

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/kite-plus/kite-kvm/internal/job"
	"github.com/kite-plus/kite-kvm/internal/model"
	"github.com/kite-plus/kite-kvm/internal/provision"
)

// Suspend stops the VM and marks it suspended (a billing suspension), recording
// the prior power state so Unsuspend can restore it.
func (s *Service) Suspend(ctx context.Context, id string) (*model.Job, error) {
	return s.enqueueOp(ctx, id, model.JobSuspend)
}

// Unsuspend reverses a suspension, restoring the prior power state.
func (s *Service) Unsuspend(ctx context.Context, id string) (*model.Job, error) {
	return s.enqueueOp(ctx, id, model.JobUnsuspend)
}

// PasswordReset re-renders the cloud-init seed with a new password. It updates
// the stored password and schedules the re-seed; the change applies on the next
// boot.
func (s *Service) PasswordReset(ctx context.Context, id, password string) (*model.Job, error) {
	if password == "" {
		return nil, fmt.Errorf("%w: password is required", ErrInvalidRequest)
	}
	v, err := s.loadOperable(ctx, id)
	if err != nil {
		return nil, err
	}
	v.Password = password
	if err := s.store.UpdateVM(ctx, v); err != nil {
		return nil, err
	}
	return s.enqueueReseed(ctx, v, model.JobPassword)
}

// SetHostname changes the VM's hostname. It updates the record and re-renders
// the cloud-init seed; the change applies on the next boot.
func (s *Service) SetHostname(ctx context.Context, id, hostname string) (*model.Job, error) {
	hostname = strings.TrimSpace(hostname)
	if hostname == "" {
		return nil, fmt.Errorf("%w: hostname is required", ErrInvalidRequest)
	}
	v, err := s.loadOperable(ctx, id)
	if err != nil {
		return nil, err
	}
	v.Hostname = hostname
	if err := s.store.UpdateVM(ctx, v); err != nil {
		return nil, err
	}
	return s.enqueueReseed(ctx, v, model.JobHostname)
}

func (s *Service) enqueueReseed(ctx context.Context, v *model.VM, typ model.JobType) (*model.Job, error) {
	j := job.New(typ, v.ID, "")
	if err := s.queue.Enqueue(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

func (s *Service) runSuspend(ctx context.Context, vmID string) error {
	v, err := s.store.GetVM(ctx, vmID)
	if err != nil {
		return err
	}
	if v.Status == model.VMStatusSuspended {
		return nil
	}

	prev := model.PowerShutoff
	if state, err := s.conn.DomainState(ctx, v.DomainName); err == nil {
		prev = mapPowerState(state)
	}
	v.PrevPowerState = prev
	if prev == model.PowerRunning || prev == model.PowerPaused {
		if err := s.conn.DestroyDomain(ctx, v.DomainName); err != nil {
			return fmt.Errorf("stop for suspend: %w", err)
		}
	}
	v.Status = model.VMStatusSuspended
	v.PowerState = model.PowerShutoff
	s.logger.Info("vm suspended", "vm_id", v.ID, "prev_power", prev)
	return s.store.UpdateVM(ctx, v)
}

func (s *Service) runUnsuspend(ctx context.Context, vmID string) error {
	v, err := s.store.GetVM(ctx, vmID)
	if err != nil {
		return err
	}
	if v.Status != model.VMStatusSuspended {
		return nil
	}

	if v.PrevPowerState == model.PowerRunning || v.PrevPowerState == model.PowerPaused {
		if err := s.conn.StartDomain(ctx, v.DomainName); err != nil {
			return fmt.Errorf("restore power on unsuspend: %w", err)
		}
		v.Status = model.VMStatusRunning
		v.PowerState = model.PowerRunning
	} else {
		v.Status = model.VMStatusStopped
		v.PowerState = model.PowerShutoff
	}
	v.PrevPowerState = model.PowerUnknown
	s.logger.Info("vm unsuspended", "vm_id", v.ID)
	return s.store.UpdateVM(ctx, v)
}

// runReseed re-renders the cloud-init seed from the current VM record (hostname,
// password, SSH keys) with a bumped instance-id so cloud-init re-applies on the
// next boot. Backs both password reset and hostname change.
func (s *Service) runReseed(ctx context.Context, vmID string) error {
	v, err := s.store.GetVM(ctx, vmID)
	if err != nil {
		return err
	}
	return s.reseedVM(ctx, v)
}

// reseedVM rebuilds the cloud-init seed ISO from a VM record with a fresh
// instance-id so cloud-init re-applies (hostname, password, network, growpart)
// on the next boot.
func (s *Service) reseedVM(ctx context.Context, v *model.VM) error {
	image, ok := s.catalog.Image(v.ImageID)
	if !ok {
		return fmt.Errorf("image %q no longer configured", v.ImageID)
	}
	if v.SeedPath == "" {
		return fmt.Errorf("vm %s has no seed to update", v.ID)
	}
	ci := provision.CloudInit{
		InstanceID:  v.ID + "-" + uuid.NewString()[:8],
		Hostname:    v.Hostname,
		DefaultUser: image.DefaultUser,
		Password:    v.Password,
		SSHKeys:     v.SSHKeys,
		Network:     s.networkConfigFor(v),
	}
	if err := provision.BuildSeedISO(v.SeedPath, ci.Files()); err != nil {
		return fmt.Errorf("rebuild seed: %w", err)
	}
	s.logger.Info("vm re-seeded", "vm_id", v.ID)
	return nil
}

// networkConfigFor reconstructs the cloud-init network config from a stored VM.
func (s *Service) networkConfigFor(v *model.VM) provision.NetworkConfig {
	nc := provision.NetworkConfig{MAC: v.MAC}
	if v.NetworkMode == model.NetworkBridge {
		nc.Static = true
		nc.Gateway = v.Gateway
		nc.AddressCIDR = v.IP + "/" + prefixFromNetmask(v.Netmask)
		if n := s.cfg.NetworkByID(v.NetworkID); n != nil {
			nc.Nameservers = n.DNS
		}
	}
	return nc
}

func prefixFromNetmask(nm string) string {
	if nm == "" {
		return "32"
	}
	ip := net.ParseIP(nm).To4()
	if ip == nil {
		return "32"
	}
	ones, _ := net.IPMask(ip).Size()
	return strconv.Itoa(ones)
}
