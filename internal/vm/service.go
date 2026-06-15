package vm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/kite-plus/kite-kvm/internal/catalog"
	"github.com/kite-plus/kite-kvm/internal/config"
	"github.com/kite-plus/kite-kvm/internal/domainxml"
	"github.com/kite-plus/kite-kvm/internal/job"
	"github.com/kite-plus/kite-kvm/internal/libvirt"
	"github.com/kite-plus/kite-kvm/internal/model"
	"github.com/kite-plus/kite-kvm/internal/network"
	"github.com/kite-plus/kite-kvm/internal/provision"
	"github.com/kite-plus/kite-kvm/internal/store"
)

const gib = 1 << 30

// Service orchestrates the VM lifecycle.
type Service struct {
	cfg         *config.Config
	store       store.Store
	conn        libvirt.Conn
	catalog     *catalog.Catalog
	network     *network.Manager
	provisioner *provision.Provisioner
	queue       *job.Queue
	logger      *slog.Logger

	statsMu   sync.Mutex
	lastStats map[string]statsSample
}

// NewService wires the dependencies and installs the job runner on the queue.
func NewService(
	cfg *config.Config,
	st store.Store,
	conn libvirt.Conn,
	cat *catalog.Catalog,
	netmgr *network.Manager,
	prov *provision.Provisioner,
	queue *job.Queue,
	logger *slog.Logger,
) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Service{
		cfg:         cfg,
		store:       st,
		conn:        conn,
		catalog:     cat,
		network:     netmgr,
		provisioner: prov,
		queue:       queue,
		logger:      logger,
		lastStats:   make(map[string]statsSample),
	}
	queue.SetRunner(s.RunJob)
	return s
}

// Create validates the request, persists a provisioning VM record, and enqueues
// the create job. The heavy lifting runs asynchronously.
func (s *Service) Create(ctx context.Context, req CreateRequest) (*model.Job, error) {
	flavor, ok := s.catalog.Flavor(req.FlavorID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrFlavorNotFound, req.FlavorID)
	}
	image, ok := s.catalog.Image(req.ImageID)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrImageNotFound, req.ImageID)
	}
	netCfg, err := s.resolveNetwork(req.Network)
	if err != nil {
		return nil, err
	}

	id := uuid.NewString()
	hostname := strings.TrimSpace(req.Hostname)
	if hostname == "" {
		hostname = "vm-" + id[:8]
	}

	vm := &model.VM{
		ID:          id,
		DomainName:  "kvm-" + id,
		Hostname:    hostname,
		FlavorID:    flavor.ID,
		ImageID:     image.ID,
		VCPUs:       flavor.VCPUs,
		MemoryMB:    flavor.MemoryMB,
		DiskGB:      flavor.DiskGB,
		NetworkID:   netCfg.ID,
		NetworkMode: model.NetworkMode(netCfg.Mode),
		Status:      model.VMStatusProvisioning,
		PowerState:  model.PowerShutoff,
		Password:    req.Password,
		SSHKeys:     req.SSHKeys,
	}
	if err := s.store.CreateVM(ctx, vm); err != nil {
		return nil, err
	}

	j := job.New(model.JobCreate, id, "")
	if err := s.queue.Enqueue(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

// StatusInfo is the lightweight current-state view of a VM.
type StatusInfo struct {
	ID         string           `json:"id"`
	Status     model.VMStatus   `json:"status"`
	PowerState model.PowerState `json:"power_state"`
}

// List returns all VMs, each with its power state reconciled against libvirt.
func (s *Service) List(ctx context.Context) ([]*model.VM, error) {
	vms, err := s.store.ListVMs(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range vms {
		s.reconcilePower(ctx, v)
	}
	return vms, nil
}

// Get returns one VM with its power state reconciled against libvirt.
func (s *Service) Get(ctx context.Context, id string) (*model.VM, error) {
	v, err := s.store.GetVM(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrVMNotFound
	}
	if err != nil {
		return nil, err
	}
	s.reconcilePower(ctx, v)
	return v, nil
}

// Status returns the VM's lifecycle status and live power state.
func (s *Service) Status(ctx context.Context, id string) (*StatusInfo, error) {
	v, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return &StatusInfo{ID: v.ID, Status: v.Status, PowerState: v.PowerState}, nil
}

// reconcilePower refreshes the in-memory VM's power state from libvirt without
// persisting (reads stay side-effect-free). Best-effort: on error the stored
// value is kept.
func (s *Service) reconcilePower(ctx context.Context, v *model.VM) {
	if v.Status == model.VMStatusTerminated {
		return
	}
	state, err := s.conn.DomainState(ctx, v.DomainName)
	if err != nil {
		return
	}
	v.PowerState = mapPowerState(state)
}

func mapPowerState(s libvirt.DomainState) model.PowerState {
	switch s {
	case libvirt.StateRunning:
		return model.PowerRunning
	case libvirt.StateShutoff:
		return model.PowerShutoff
	case libvirt.StatePaused:
		return model.PowerPaused
	default:
		return model.PowerUnknown
	}
}

// loadOperable fetches a VM and rejects operations on terminated VMs.
func (s *Service) loadOperable(ctx context.Context, id string) (*model.VM, error) {
	v, err := s.store.GetVM(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrVMNotFound
	}
	if err != nil {
		return nil, err
	}
	if v.Status == model.VMStatusTerminated {
		return nil, ErrVMTerminated
	}
	return v, nil
}

// RunJob is the queue runner: it dispatches by job type.
func (s *Service) RunJob(ctx context.Context, j *model.Job) error {
	switch j.Type {
	case model.JobCreate:
		return s.runCreate(ctx, j.VMID)
	case model.JobStart, model.JobShutdown, model.JobReboot, model.JobStop:
		return s.runPower(ctx, j.VMID, j.Type)
	case model.JobTerminate:
		return s.runTerminate(ctx, j.VMID)
	case model.JobSuspend:
		return s.runSuspend(ctx, j.VMID)
	case model.JobUnsuspend:
		return s.runUnsuspend(ctx, j.VMID)
	case model.JobPassword, model.JobHostname:
		return s.runReseed(ctx, j.VMID)
	case model.JobRebuild:
		return s.runRebuild(ctx, j.VMID)
	case model.JobResize:
		return s.runResize(ctx, j.VMID)
	default:
		return fmt.Errorf("unsupported job type %q", j.Type)
	}
}

// Terminate schedules full teardown of a VM. It is idempotent: terminating an
// already-terminated VM succeeds as a no-op.
func (s *Service) Terminate(ctx context.Context, id string) (*model.Job, error) {
	if _, err := s.store.GetVM(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrVMNotFound
		}
		return nil, err
	}
	j := job.New(model.JobTerminate, id, "")
	if err := s.queue.Enqueue(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

// runTerminate destroys and undefines the domain, deletes the overlay disk and
// seed, releases the MAC/IP allocation, and marks the VM terminated. Every step
// is best-effort and idempotent so retries and partial states converge cleanly.
func (s *Service) runTerminate(ctx context.Context, vmID string) error {
	v, err := s.store.GetVM(ctx, vmID)
	if err != nil {
		return err
	}
	if v.Status == model.VMStatusTerminated {
		return nil
	}
	s.teardownPartial(ctx, v, v.MAC)
	v.Status = model.VMStatusTerminated
	v.PowerState = model.PowerShutoff
	s.logger.Info("vm terminated", "vm_id", v.ID)
	return s.store.UpdateVM(ctx, v)
}

func (s *Service) runCreate(ctx context.Context, vmID string) error {
	vm, err := s.store.GetVM(ctx, vmID)
	if err != nil {
		return err
	}
	image, ok := s.catalog.Image(vm.ImageID)
	if !ok {
		return s.failVM(ctx, vm, fmt.Errorf("image %q no longer configured", vm.ImageID))
	}

	// 1. Allocate the network (MAC/IP + host wiring).
	att, err := s.network.Allocate(ctx, vm.NetworkID, vm.ID, vm.Hostname)
	if err != nil {
		return s.failVM(ctx, vm, fmt.Errorf("allocate network: %w", err))
	}
	vm.MAC = att.MAC
	vm.IP = att.IP
	vm.Gateway = att.Gateway
	vm.Netmask = att.Netmask
	_ = s.store.UpdateVM(ctx, vm)

	// 2. Build the overlay disk and cloud-init seed.
	art, err := s.provisioner.Prepare(ctx, provision.PrepareRequest{
		ID:          vm.ID,
		Hostname:    vm.Hostname,
		DefaultUser: image.DefaultUser,
		Password:    vm.Password,
		SSHKeys:     vm.SSHKeys,
		BackingPath: image.BasePath,
		DiskBytes:   uint64(vm.DiskGB) * gib,
		Network: provision.NetworkConfig{
			MAC:         att.MAC,
			Static:      att.Static,
			AddressCIDR: att.AddressCIDR,
			Gateway:     att.Gateway,
			Nameservers: att.Nameservers,
		},
	})
	if err != nil {
		return s.failCreate(ctx, vm, att.MAC, fmt.Errorf("provision: %w", err))
	}
	vm.DiskPath = art.DiskPath
	vm.SeedPath = art.SeedPath

	// 3. Render and define the domain.
	xml, err := domainxml.Render(domainxml.Spec{
		Name:          vm.DomainName,
		VCPUs:         vm.VCPUs,
		MemoryMB:      vm.MemoryMB,
		DiskPath:      art.DiskPath,
		SeedPath:      art.SeedPath,
		MAC:           att.MAC,
		Network:       domainxml.NetworkAttachment{Mode: att.Mode, Source: att.Source, VLAN: att.VLAN},
		BandwidthMbps: s.flavorBandwidth(vm.FlavorID),
	})
	if err != nil {
		return s.failCreate(ctx, vm, att.MAC, fmt.Errorf("render domain xml: %w", err))
	}
	domUUID, err := s.conn.DefineDomain(ctx, xml)
	if err != nil {
		return s.failCreate(ctx, vm, att.MAC, fmt.Errorf("define domain: %w", err))
	}
	vm.DomainUUID = domUUID

	// 4. Start the domain.
	if err := s.conn.StartDomain(ctx, vm.DomainName); err != nil {
		return s.failCreate(ctx, vm, att.MAC, fmt.Errorf("start domain: %w", err))
	}

	vm.Status = model.VMStatusRunning
	vm.PowerState = model.PowerRunning
	s.logger.Info("vm provisioned", "vm_id", vm.ID, "ip", vm.IP, "network", vm.NetworkID)
	return s.store.UpdateVM(ctx, vm)
}

func (s *Service) resolveNetwork(req NetworkRequest) (*config.Network, error) {
	if req.NetworkID != "" {
		n := s.cfg.NetworkByID(req.NetworkID)
		if n == nil {
			return nil, fmt.Errorf("%w: %s", ErrNetworkNotFound, req.NetworkID)
		}
		return n, nil
	}
	if req.Mode != "" {
		var first *config.Network
		for i := range s.cfg.Networks {
			n := &s.cfg.Networks[i]
			if n.Mode != req.Mode {
				continue
			}
			if n.Default {
				return n, nil
			}
			if first == nil {
				first = n
			}
		}
		if first != nil {
			return first, nil
		}
		return nil, fmt.Errorf("%w: no network with mode %q", ErrNetworkNotFound, req.Mode)
	}
	if d := s.cfg.DefaultNetwork(); d != nil {
		return d, nil
	}
	return nil, ErrNetworkNotFound
}

func (s *Service) flavorBandwidth(flavorID string) int {
	if f, ok := s.catalog.Flavor(flavorID); ok {
		return f.BandwidthMbps
	}
	return 0
}

// networkAttachmentFor reconstructs the domain-XML network attachment from a
// stored VM and its configured network.
func (s *Service) networkAttachmentFor(v *model.VM) domainxml.NetworkAttachment {
	att := domainxml.NetworkAttachment{}
	n := s.cfg.NetworkByID(v.NetworkID)
	if v.NetworkMode == model.NetworkBridge {
		att.Mode = domainxml.ModeBridge
		if n != nil {
			att.Source = n.Bridge
			att.VLAN = n.VLAN
		}
	} else {
		att.Mode = domainxml.ModeNAT
		if n != nil {
			att.Source = n.LibvirtNetwork
		}
	}
	return att
}

// buildDomainSpec reconstructs the full domain spec from a stored VM, for
// redefining the domain after a rebuild or resize.
func (s *Service) buildDomainSpec(v *model.VM) domainxml.Spec {
	return domainxml.Spec{
		Name:          v.DomainName,
		VCPUs:         v.VCPUs,
		MemoryMB:      v.MemoryMB,
		DiskPath:      v.DiskPath,
		SeedPath:      v.SeedPath,
		MAC:           v.MAC,
		Network:       s.networkAttachmentFor(v),
		BandwidthMbps: s.flavorBandwidth(v.FlavorID),
	}
}

// failVM marks the VM errored and returns the cause (no resources to reclaim).
func (s *Service) failVM(ctx context.Context, vm *model.VM, cause error) error {
	vm.Status = model.VMStatusError
	_ = s.store.UpdateVM(ctx, vm)
	return cause
}

// failCreate tears down any partial resources, marks the VM errored, and
// returns the cause. Every step is best-effort and idempotent.
func (s *Service) failCreate(ctx context.Context, vm *model.VM, mac string, cause error) error {
	s.teardownPartial(ctx, vm, mac)
	return s.failVM(ctx, vm, cause)
}

func (s *Service) teardownPartial(ctx context.Context, vm *model.VM, mac string) {
	_ = s.conn.DestroyDomain(ctx, vm.DomainName)
	_ = s.conn.UndefineDomain(ctx, vm.DomainName)
	_ = s.conn.DeleteVolume(ctx, s.cfg.Libvirt.StoragePool, vm.ID+".qcow2")
	if vm.SeedPath != "" {
		_ = os.Remove(vm.SeedPath)
	}
	_ = s.network.Release(ctx, vm.NetworkID, vm.ID, mac)
}
