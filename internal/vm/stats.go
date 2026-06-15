package vm

import (
	"context"
	"errors"
	"time"

	"github.com/kite-plus/kite-kvm/internal/libvirt"
	"github.com/kite-plus/kite-kvm/internal/model"
	"github.com/kite-plus/kite-kvm/internal/store"
)

// statsSample is a previous stats reading used to compute rates.
type statsSample struct {
	t time.Time
	s libvirt.DomainStats
}

// StatsInfo is a point-in-time resource usage reading for a VM. Rate fields are
// computed against the previous reading (per-process, in memory) and are zero
// on the first call or after a counter reset.
type StatsInfo struct {
	ID                   string           `json:"id"`
	PowerState           model.PowerState `json:"power_state"`
	CPUTimeNs            uint64           `json:"cpu_time_ns"`
	CPUPercent           float64          `json:"cpu_percent"`
	MemBalloonKiB        uint64           `json:"mem_balloon_kib"`
	MemRSSKiB            uint64           `json:"mem_rss_kib"`
	NetRxBytes           uint64           `json:"net_rx_bytes"`
	NetTxBytes           uint64           `json:"net_tx_bytes"`
	NetRxBps             float64          `json:"net_rx_bps"`
	NetTxBps             float64          `json:"net_tx_bps"`
	BlockRdBytes         uint64           `json:"block_rd_bytes"`
	BlockWrBytes         uint64           `json:"block_wr_bytes"`
	BlockRdBps           float64          `json:"block_rd_bps"`
	BlockWrBps           float64          `json:"block_wr_bps"`
	BlockAllocationBytes uint64           `json:"block_allocation_bytes"`
	BlockCapacityBytes   uint64           `json:"block_capacity_bytes"`
	IntervalSeconds      float64          `json:"interval_seconds"`
}

// Stats returns live resource usage for a VM, including interval rates computed
// against the previous reading.
func (s *Service) Stats(ctx context.Context, id string) (*StatsInfo, error) {
	v, err := s.store.GetVM(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrVMNotFound
	}
	if err != nil {
		return nil, err
	}

	all, err := s.conn.AllDomainStats(ctx)
	if err != nil {
		return nil, err
	}
	var cur *libvirt.DomainStats
	for i := range all {
		if all[i].Name == v.DomainName {
			cur = &all[i]
			break
		}
	}
	if cur == nil {
		// No live stats (e.g. the domain is shut off): report just the state.
		s.reconcilePower(ctx, v)
		return &StatsInfo{ID: v.ID, PowerState: v.PowerState}, nil
	}

	info := &StatsInfo{
		ID:                   v.ID,
		PowerState:           mapPowerState(cur.State),
		CPUTimeNs:            cur.CPUTimeNs,
		MemBalloonKiB:        cur.MemBalloonKiB,
		MemRSSKiB:            cur.MemRSSKiB,
		NetRxBytes:           cur.NetRxBytes,
		NetTxBytes:           cur.NetTxBytes,
		BlockRdBytes:         cur.BlockRdBytes,
		BlockWrBytes:         cur.BlockWrBytes,
		BlockAllocationBytes: cur.BlockAllocation,
		BlockCapacityBytes:   cur.BlockCapacity,
	}

	now := time.Now()
	s.statsMu.Lock()
	prev, ok := s.lastStats[v.DomainName]
	s.lastStats[v.DomainName] = statsSample{t: now, s: *cur}
	s.statsMu.Unlock()

	if ok {
		elapsed := now.Sub(prev.t).Seconds()
		if elapsed > 0 {
			info.IntervalSeconds = elapsed
			info.CPUPercent = cpuPercent(prev.s.CPUTimeNs, cur.CPUTimeNs, elapsed, v.VCPUs)
			info.NetRxBps = rate(prev.s.NetRxBytes, cur.NetRxBytes, elapsed)
			info.NetTxBps = rate(prev.s.NetTxBytes, cur.NetTxBytes, elapsed)
			info.BlockRdBps = rate(prev.s.BlockRdBytes, cur.BlockRdBytes, elapsed)
			info.BlockWrBps = rate(prev.s.BlockWrBytes, cur.BlockWrBytes, elapsed)
		}
	}
	return info, nil
}

// rate returns bytes/sec, treating a counter reset (cur < prev) as zero.
func rate(prev, cur uint64, elapsed float64) float64 {
	if cur < prev || elapsed <= 0 {
		return 0
	}
	return float64(cur-prev) / elapsed
}

// cpuPercent returns CPU utilization across all vCPUs as a percentage.
func cpuPercent(prevNs, curNs uint64, elapsed float64, vcpus int) float64 {
	if curNs < prevNs || elapsed <= 0 || vcpus <= 0 {
		return 0
	}
	delta := float64(curNs - prevNs)
	return delta / (elapsed * 1e9 * float64(vcpus)) * 100
}
