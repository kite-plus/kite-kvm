package vm

import (
	"context"
	"fmt"
	"time"

	"github.com/kite-plus/kite-kvm/internal/domainxml"
	"github.com/kite-plus/kite-kvm/internal/job"
	"github.com/kite-plus/kite-kvm/internal/libvirt"
	"github.com/kite-plus/kite-kvm/internal/model"
)

// Block reasons distinguish an automatic quota cutoff from a manual admin block,
// so the accountant only auto-restores the quota ones.
const (
	blockReasonQuota  = "quota"
	blockReasonManual = "manual"
)

// netCounters is a domain's last-seen cumulative rx/tx byte counters.
type netCounters struct{ rx, tx uint64 }

// TrafficInfo is the usage view for a VM (combined in+out).
type TrafficInfo struct {
	ID          string    `json:"id"`
	QuotaBytes  uint64    `json:"quota_bytes"`
	UsedBytes   uint64    `json:"used_bytes"`
	Unlimited   bool      `json:"unlimited"`
	Percent     float64   `json:"percent"`
	PeriodStart time.Time `json:"period_start"`
	Blocked     bool      `json:"blocked"`
	BlockReason string    `json:"block_reason,omitempty"`
}

func trafficInfo(v *model.VM) *TrafficInfo {
	info := &TrafficInfo{
		ID:          v.ID,
		QuotaBytes:  v.TrafficQuotaBytes,
		UsedBytes:   v.TrafficUsedBytes,
		Unlimited:   v.TrafficQuotaBytes == 0,
		PeriodStart: v.TrafficPeriodStart,
		Blocked:     v.NetworkBlocked,
		BlockReason: v.NetworkBlockReason,
	}
	if v.TrafficQuotaBytes > 0 {
		info.Percent = float64(v.TrafficUsedBytes) / float64(v.TrafficQuotaBytes) * 100
	}
	return info
}

// TrafficUsage returns a VM's current traffic usage.
func (s *Service) TrafficUsage(ctx context.Context, id string) (*TrafficInfo, error) {
	v, err := s.loadOperable(ctx, id)
	if err != nil {
		return nil, err
	}
	return trafficInfo(v), nil
}

// SetTrafficQuota sets a VM's combined in+out quota (GB; 0 = unlimited) and
// reconciles the block state. Synchronous.
func (s *Service) SetTrafficQuota(ctx context.Context, id string, quotaGB int) (*TrafficInfo, error) {
	if quotaGB < 0 {
		return nil, fmt.Errorf("%w: quota_gb must be >= 0", ErrInvalidRequest)
	}
	v, err := s.loadOperable(ctx, id)
	if err != nil {
		return nil, err
	}
	v.TrafficQuotaBytes = uint64(quotaGB) * gib
	s.reconcileQuota(ctx, v) // may lift/raise a quota block
	if err := s.store.UpdateVM(ctx, v); err != nil {
		return nil, err
	}
	return trafficInfo(v), nil
}

// BlockNetwork manually cuts a VM's network. ResetTraffic / unblock restore it.
func (s *Service) BlockNetwork(ctx context.Context, id string) (*model.Job, error) {
	return s.enqueueVMJob(ctx, id, model.JobNetBlock)
}

// UnblockNetwork manually restores a VM's network.
func (s *Service) UnblockNetwork(ctx context.Context, id string) (*model.Job, error) {
	return s.enqueueVMJob(ctx, id, model.JobNetUnblock)
}

// ResetTraffic zeroes usage, starts a new period, and lifts a quota block.
func (s *Service) ResetTraffic(ctx context.Context, id string) (*model.Job, error) {
	return s.enqueueVMJob(ctx, id, model.JobTrafficReset)
}

func (s *Service) enqueueVMJob(ctx context.Context, id string, typ model.JobType) (*model.Job, error) {
	if _, err := s.loadOperable(ctx, id); err != nil {
		return nil, err
	}
	j := job.New(typ, id, "")
	if err := s.queue.Enqueue(ctx, j); err != nil {
		return nil, err
	}
	return j, nil
}

func (s *Service) runNetBlock(ctx context.Context, vmID string) error {
	v, err := s.store.GetVM(ctx, vmID)
	if err != nil {
		return err
	}
	v.NetworkBlocked = true
	v.NetworkBlockReason = blockReasonManual
	_ = s.applyLink(ctx, v)
	s.logger.Info("vm network blocked (manual)", "vm_id", v.ID)
	return s.store.UpdateVM(ctx, v)
}

func (s *Service) runNetUnblock(ctx context.Context, vmID string) error {
	v, err := s.store.GetVM(ctx, vmID)
	if err != nil {
		return err
	}
	v.NetworkBlocked = false
	v.NetworkBlockReason = ""
	_ = s.applyLink(ctx, v)
	s.logger.Info("vm network unblocked", "vm_id", v.ID)
	return s.store.UpdateVM(ctx, v)
}

func (s *Service) runTrafficReset(ctx context.Context, vmID string) error {
	v, err := s.store.GetVM(ctx, vmID)
	if err != nil {
		return err
	}
	v.TrafficUsedBytes = 0
	v.TrafficPeriodStart = time.Now().UTC()
	if v.NetworkBlocked && v.NetworkBlockReason == blockReasonQuota {
		v.NetworkBlocked = false
		v.NetworkBlockReason = ""
		_ = s.applyLink(ctx, v)
	}
	s.trafficMu.Lock()
	delete(s.trafficLast, v.DomainName) // reseed the counter baseline
	s.trafficMu.Unlock()
	s.logger.Info("vm traffic reset", "vm_id", v.ID)
	return s.store.UpdateVM(ctx, v)
}

// applyLink makes the running domain's NIC link match v.NetworkBlocked. It is a
// no-op when the domain is not running: the state is baked into the domain XML
// on define and re-applied on start.
func (s *Service) applyLink(ctx context.Context, v *model.VM) error {
	state, err := s.conn.DomainState(ctx, v.DomainName)
	if err != nil || mapPowerState(state) != model.PowerRunning {
		return nil
	}
	ifaceXML, err := domainxml.RenderInterface(domainxml.Spec{
		MAC:           v.MAC,
		Network:       s.networkAttachmentFor(v),
		BandwidthMbps: s.flavorBandwidth(v.FlavorID),
		LinkDown:      v.NetworkBlocked,
	})
	if err != nil {
		return err
	}
	return s.conn.UpdateInterface(ctx, v.DomainName, ifaceXML)
}

// reconcileQuota blocks an over-quota VM or restores a quota-blocked VM that is
// back under quota. It mutates v in place and returns whether the block state
// changed (so the caller can persist).
func (s *Service) reconcileQuota(ctx context.Context, v *model.VM) bool {
	if v.Status == model.VMStatusTerminated {
		return false
	}
	// Unlimited: lift any prior quota block.
	if v.TrafficQuotaBytes == 0 {
		if v.NetworkBlocked && v.NetworkBlockReason == blockReasonQuota {
			v.NetworkBlocked = false
			v.NetworkBlockReason = ""
			_ = s.applyLink(ctx, v)
			return true
		}
		return false
	}

	over := v.TrafficUsedBytes >= v.TrafficQuotaBytes
	switch {
	case over && !v.NetworkBlocked:
		v.NetworkBlocked = true
		v.NetworkBlockReason = blockReasonQuota
		_ = s.applyLink(ctx, v)
		s.logger.Warn("vm network cut: traffic quota exhausted",
			"vm_id", v.ID, "used_bytes", v.TrafficUsedBytes, "quota_bytes", v.TrafficQuotaBytes)
		return true
	case !over && v.NetworkBlocked && v.NetworkBlockReason == blockReasonQuota:
		v.NetworkBlocked = false
		v.NetworkBlockReason = ""
		_ = s.applyLink(ctx, v)
		s.logger.Info("vm network restored: back under quota", "vm_id", v.ID)
		return true
	}
	return false
}

// AccountTraffic runs the periodic traffic-accounting loop until ctx is done.
func (s *Service) AccountTraffic(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	s.logger.Info("traffic accounting started", "interval", interval.String())
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.accountTick(ctx)
		}
	}
}

// accountTick samples bulk domain stats, accumulates the reset-aware combined
// in+out delta per VM, and enforces quotas.
func (s *Service) accountTick(ctx context.Context) {
	stats, err := s.conn.AllDomainStats(ctx)
	if err != nil {
		s.logger.Warn("traffic accounting: stats unavailable", "error", err)
		return
	}
	byDomain := make(map[string]libvirt.DomainStats, len(stats))
	for _, st := range stats {
		byDomain[st.Name] = st
	}

	vms, err := s.store.ListVMs(ctx)
	if err != nil {
		return
	}
	for _, v := range vms {
		if v.Status == model.VMStatusTerminated {
			continue
		}
		changed := false
		if st, ok := byDomain[v.DomainName]; ok {
			s.trafficMu.Lock()
			prev, seen := s.trafficLast[v.DomainName]
			s.trafficLast[v.DomainName] = netCounters{rx: st.NetRxBytes, tx: st.NetTxBytes}
			s.trafficMu.Unlock()
			// First sighting only seeds the baseline (avoids counting historical
			// bytes since vnet creation as fresh usage).
			if seen {
				delta := resetAwareDelta(prev.rx, st.NetRxBytes) + resetAwareDelta(prev.tx, st.NetTxBytes)
				if delta > 0 {
					v.TrafficUsedBytes += delta
					changed = true
				}
			}
		}
		if s.reconcileQuota(ctx, v) {
			changed = true
		}
		if changed {
			_ = s.store.UpdateVM(ctx, v)
		}
	}
}

// resetAwareDelta returns cur-prev, treating a counter reset (cur < prev, e.g.
// the vnet device was recreated on VM restart) as counting from zero.
func resetAwareDelta(prev, cur uint64) uint64 {
	if cur < prev {
		return cur
	}
	return cur - prev
}
