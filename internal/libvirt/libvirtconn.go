package libvirt

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	golibvirt "github.com/digitalocean/go-libvirt"
	"github.com/digitalocean/go-libvirt/socket/dialers"
)

// libvirtConn is the go-libvirt-backed Conn. It talks to the local libvirtd over
// the unix socket and (re)connects lazily.
type libvirtConn struct {
	uri string

	mu sync.Mutex
	l  *golibvirt.Libvirt
}

var _ Conn = (*libvirtConn)(nil)

func newLibvirtConn(uri string) *libvirtConn { return &libvirtConn{uri: uri} }

// ensure returns a connected client, dialing the local socket on first use or
// after a disconnect.
func (c *libvirtConn) ensure(context.Context) (*golibvirt.Libvirt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.l != nil && c.l.IsConnected() {
		return c.l, nil
	}
	l := golibvirt.NewWithDialer(dialers.NewLocal())
	if err := l.ConnectToURI(golibvirt.ConnectURI(c.uri)); err != nil {
		return nil, fmt.Errorf("connect %s: %w", c.uri, err)
	}
	c.l = l
	return c.l, nil
}

func (c *libvirtConn) Connect(ctx context.Context) error {
	_, err := c.ensure(ctx)
	return err
}

func (c *libvirtConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.l == nil {
		return nil
	}
	err := c.l.Disconnect()
	c.l = nil
	return err
}

func (c *libvirtConn) Ping(ctx context.Context) error {
	l, err := c.ensure(ctx)
	if err != nil {
		return err
	}
	if _, err := l.ConnectGetLibVersion(); err != nil {
		c.reset()
		return err
	}
	return nil
}

func (c *libvirtConn) reset() {
	c.mu.Lock()
	c.l = nil
	c.mu.Unlock()
}

// lookup resolves a domain by name, mapping libvirt's not-found to
// ErrDomainNotFound.
func (c *libvirtConn) lookup(ctx context.Context, name string) (*golibvirt.Libvirt, golibvirt.Domain, error) {
	l, err := c.ensure(ctx)
	if err != nil {
		return nil, golibvirt.Domain{}, err
	}
	dom, err := l.DomainLookupByName(name)
	if err != nil {
		if golibvirt.IsNotFound(err) {
			return nil, golibvirt.Domain{}, ErrDomainNotFound
		}
		return nil, golibvirt.Domain{}, err
	}
	return l, dom, nil
}

func (c *libvirtConn) DefineDomain(ctx context.Context, xml string) (string, error) {
	l, err := c.ensure(ctx)
	if err != nil {
		return "", err
	}
	dom, err := l.DomainDefineXML(xml)
	if err != nil {
		return "", err
	}
	return formatUUID(dom.UUID), nil
}

func (c *libvirtConn) StartDomain(ctx context.Context, name string) error {
	l, dom, err := c.lookup(ctx, name)
	if err != nil {
		return err
	}
	return l.DomainCreate(dom)
}

func (c *libvirtConn) ShutdownDomain(ctx context.Context, name string) error {
	l, dom, err := c.lookup(ctx, name)
	if err != nil {
		return err
	}
	return l.DomainShutdown(dom)
}

func (c *libvirtConn) RebootDomain(ctx context.Context, name string) error {
	l, dom, err := c.lookup(ctx, name)
	if err != nil {
		return err
	}
	return l.DomainReboot(dom, 0)
}

func (c *libvirtConn) DestroyDomain(ctx context.Context, name string) error {
	l, dom, err := c.lookup(ctx, name)
	if err != nil {
		return err
	}
	return l.DomainDestroy(dom)
}

func (c *libvirtConn) SuspendDomain(ctx context.Context, name string) error {
	l, dom, err := c.lookup(ctx, name)
	if err != nil {
		return err
	}
	return l.DomainSuspend(dom)
}

func (c *libvirtConn) ResumeDomain(ctx context.Context, name string) error {
	l, dom, err := c.lookup(ctx, name)
	if err != nil {
		return err
	}
	return l.DomainResume(dom)
}

func (c *libvirtConn) UndefineDomain(ctx context.Context, name string) error {
	l, dom, err := c.lookup(ctx, name)
	if err != nil {
		return err
	}
	return l.DomainUndefine(dom)
}

func (c *libvirtConn) DomainState(ctx context.Context, name string) (DomainState, error) {
	l, dom, err := c.lookup(ctx, name)
	if err != nil {
		return StateUnknown, err
	}
	state, _, err := l.DomainGetState(dom, 0)
	if err != nil {
		return StateUnknown, err
	}
	return mapState(state), nil
}

func (c *libvirtConn) ListDomains(ctx context.Context) ([]string, error) {
	l, err := c.ensure(ctx)
	if err != nil {
		return nil, err
	}
	doms, _, err := l.ConnectListAllDomains(1, golibvirt.ConnectListDomainsActive|golibvirt.ConnectListDomainsInactive)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(doms))
	for _, d := range doms {
		names = append(names, d.Name)
	}
	return names, nil
}

func (c *libvirtConn) DomainXML(ctx context.Context, name string) (string, error) {
	l, dom, err := c.lookup(ctx, name)
	if err != nil {
		return "", err
	}
	return l.DomainGetXMLDesc(dom, 0)
}

func (c *libvirtConn) UpdateInterface(ctx context.Context, domain, ifaceXML string) error {
	l, dom, err := c.lookup(ctx, domain)
	if err != nil {
		return err
	}
	// Live: apply to the running domain now. Config: persist so a later plain
	// stop/start keeps the same link state.
	flags := golibvirt.DomainDeviceModifyLive | golibvirt.DomainDeviceModifyConfig
	return l.DomainUpdateDeviceFlags(dom, ifaceXML, flags)
}

func (c *libvirtConn) DomainVNCAddress(ctx context.Context, name string) (string, int, error) {
	l, dom, err := c.lookup(ctx, name)
	if err != nil {
		return "", 0, err
	}
	desc, err := l.DomainGetXMLDesc(dom, 0)
	if err != nil {
		return "", 0, err
	}
	return parseVNCFromXML(desc)
}

func (c *libvirtConn) CreateVolume(ctx context.Context, spec StorageVolSpec) (string, error) {
	l, err := c.ensure(ctx)
	if err != nil {
		return "", err
	}
	pool, err := l.StoragePoolLookupByName(spec.Pool)
	if err != nil {
		return "", fmt.Errorf("lookup pool %q: %w", spec.Pool, err)
	}
	vol, err := l.StorageVolCreateXML(pool, buildVolumeXML(spec), 0)
	if err != nil {
		return "", err
	}
	return l.StorageVolGetPath(vol)
}

func (c *libvirtConn) DeleteVolume(ctx context.Context, pool, name string) error {
	l, err := c.ensure(ctx)
	if err != nil {
		return err
	}
	p, err := l.StoragePoolLookupByName(pool)
	if err != nil {
		if isLibvirtErr(err, golibvirt.ErrNoStoragePool) {
			return nil
		}
		return err
	}
	vol, err := l.StorageVolLookupByName(p, name)
	if err != nil {
		if isLibvirtErr(err, golibvirt.ErrNoStorageVol) {
			return nil // already gone
		}
		return err
	}
	return l.StorageVolDelete(vol, 0)
}

func (c *libvirtConn) ResizeVolume(ctx context.Context, pool, name string, capacityBytes uint64) error {
	l, err := c.ensure(ctx)
	if err != nil {
		return err
	}
	p, err := l.StoragePoolLookupByName(pool)
	if err != nil {
		return fmt.Errorf("lookup pool %q: %w", pool, err)
	}
	vol, err := l.StorageVolLookupByName(p, name)
	if err != nil {
		return fmt.Errorf("lookup volume %q: %w", name, err)
	}
	return l.StorageVolResize(vol, capacityBytes, 0)
}

func (c *libvirtConn) AllDomainStats(ctx context.Context) ([]DomainStats, error) {
	l, err := c.ensure(ctx)
	if err != nil {
		return nil, err
	}
	statsFlags := uint32(golibvirt.DomainStatsState |
		golibvirt.DomainStatsCPUTotal |
		golibvirt.DomainStatsBalloon |
		golibvirt.DomainStatsInterface |
		golibvirt.DomainStatsBlock)
	records, err := l.ConnectGetAllDomainStats(nil, statsFlags, 0)
	if err != nil {
		return nil, err
	}
	out := make([]DomainStats, 0, len(records))
	for _, rec := range records {
		out = append(out, parseStats(rec))
	}
	return out, nil
}

func (c *libvirtConn) AddDHCPHost(ctx context.Context, network, mac, name, ip string) error {
	l, net, err := c.lookupNetwork(ctx, network)
	if err != nil {
		return err
	}
	xml := fmt.Sprintf(`<host mac='%s' name='%s' ip='%s'/>`, mac, name, ip)
	return l.NetworkUpdate(net,
		uint32(golibvirt.NetworkUpdateCommandAddLast),
		uint32(golibvirt.NetworkSectionIPDhcpHost),
		-1, xml,
		golibvirt.NetworkUpdateAffectLive|golibvirt.NetworkUpdateAffectConfig)
}

func (c *libvirtConn) RemoveDHCPHost(ctx context.Context, network, mac string) error {
	l, net, err := c.lookupNetwork(ctx, network)
	if err != nil {
		return err
	}
	xml := fmt.Sprintf(`<host mac='%s'/>`, mac)
	err = l.NetworkUpdate(net,
		uint32(golibvirt.NetworkUpdateCommandDelete),
		uint32(golibvirt.NetworkSectionIPDhcpHost),
		-1, xml,
		golibvirt.NetworkUpdateAffectLive|golibvirt.NetworkUpdateAffectConfig)
	if err != nil && isLibvirtErr(err, golibvirt.ErrOperationInvalid) {
		return nil // lease was not present
	}
	return err
}

func (c *libvirtConn) lookupNetwork(ctx context.Context, name string) (*golibvirt.Libvirt, golibvirt.Network, error) {
	l, err := c.ensure(ctx)
	if err != nil {
		return nil, golibvirt.Network{}, err
	}
	net, err := l.NetworkLookupByName(name)
	if err != nil {
		return nil, golibvirt.Network{}, fmt.Errorf("lookup network %q: %w", name, err)
	}
	return l, net, nil
}

// --- helpers ---------------------------------------------------------------

func mapState(state int32) DomainState {
	switch golibvirt.DomainState(state) {
	case golibvirt.DomainRunning:
		return StateRunning
	case golibvirt.DomainPaused:
		return StatePaused
	case golibvirt.DomainShutoff, golibvirt.DomainShutdown, golibvirt.DomainCrashed:
		return StateShutoff
	default:
		return StateUnknown
	}
}

func formatUUID(u golibvirt.UUID) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", u[0:4], u[4:6], u[6:8], u[8:10], u[10:16])
}

func buildVolumeXML(spec StorageVolSpec) string {
	var b strings.Builder
	b.WriteString(`<volume type='file'>`)
	fmt.Fprintf(&b, `<name>%s</name>`, spec.Name)
	fmt.Fprintf(&b, `<capacity unit='bytes'>%d</capacity>`, spec.CapacityBytes)
	b.WriteString(`<target><format type='qcow2'/></target>`)
	if spec.BackingPath != "" {
		backingFmt := spec.BackingFmt
		if backingFmt == "" {
			backingFmt = "qcow2"
		}
		fmt.Fprintf(&b, `<backingStore><path>%s</path><format type='%s'/></backingStore>`, spec.BackingPath, backingFmt)
	}
	b.WriteString(`</volume>`)
	return b.String()
}

func parseStats(rec golibvirt.DomainStatsRecord) DomainStats {
	s := DomainStats{Name: rec.Dom.Name, State: StateUnknown}
	for _, p := range rec.Params {
		v := tpUint64(p.Value)
		switch {
		case p.Field == "state.state":
			s.State = mapState(int32(v))
		case p.Field == "cpu.time":
			s.CPUTimeNs = v
		case p.Field == "balloon.current":
			s.MemBalloonKiB = v
		case p.Field == "balloon.rss":
			s.MemRSSKiB = v
		case strings.HasPrefix(p.Field, "net.") && strings.HasSuffix(p.Field, ".rx.bytes"):
			s.NetRxBytes += v
		case strings.HasPrefix(p.Field, "net.") && strings.HasSuffix(p.Field, ".tx.bytes"):
			s.NetTxBytes += v
		case strings.HasPrefix(p.Field, "block.") && strings.HasSuffix(p.Field, ".rd.bytes"):
			s.BlockRdBytes += v
		case strings.HasPrefix(p.Field, "block.") && strings.HasSuffix(p.Field, ".wr.bytes"):
			s.BlockWrBytes += v
		case strings.HasPrefix(p.Field, "block.") && strings.HasSuffix(p.Field, ".allocation"):
			s.BlockAllocation += v
		case strings.HasPrefix(p.Field, "block.") && strings.HasSuffix(p.Field, ".capacity"):
			s.BlockCapacity += v
		}
	}
	return s
}

func tpUint64(v golibvirt.TypedParamValue) uint64 {
	switch n := v.I.(type) {
	case int:
		if n < 0 {
			return 0
		}
		return uint64(n)
	case int32:
		if n < 0 {
			return 0
		}
		return uint64(n)
	case int64:
		if n < 0 {
			return 0
		}
		return uint64(n)
	case uint:
		return uint64(n)
	case uint32:
		return uint64(n)
	case uint64:
		return n
	case float64:
		if n < 0 {
			return 0
		}
		return uint64(n)
	default:
		return 0
	}
}

func (c *libvirtConn) HostInfo(ctx context.Context, pool string) (HostInfo, error) {
	l, err := c.ensure(ctx)
	if err != nil {
		return HostInfo{}, err
	}
	_, memKiB, cpus, _, _, _, _, _, err := l.NodeGetInfo()
	if err != nil {
		return HostInfo{}, fmt.Errorf("node info: %w", err)
	}
	info := HostInfo{
		CPUs:             int(cpus),
		MemoryTotalBytes: memKiB * 1024,
	}
	if free, err := l.NodeGetFreeMemory(); err == nil {
		info.MemoryFreeBytes = free
	}
	if hn, err := l.ConnectGetHostname(); err == nil {
		info.Hostname = hn
	}
	if ver, err := l.ConnectGetLibVersion(); err == nil {
		info.LibvirtVersion = formatLibvirtVersion(ver)
	}
	if p, err := l.StoragePoolLookupByName(pool); err == nil {
		if _, capacity, _, available, err := l.StoragePoolGetInfo(p); err == nil {
			info.StorageBytes = capacity
			info.StorageFreeBytes = available
		}
	}
	return info, nil
}

// formatLibvirtVersion turns libvirt's packed version (major*1e6 + minor*1e3 +
// release) into "major.minor.release".
func formatLibvirtVersion(v uint64) string {
	return fmt.Sprintf("%d.%d.%d", v/1000000, (v/1000)%1000, v%1000)
}

func (c *libvirtConn) CreateSnapshot(ctx context.Context, domain, name, description string) error {
	l, dom, err := c.lookup(ctx, domain)
	if err != nil {
		return err
	}
	_, err = l.DomainSnapshotCreateXML(dom, buildSnapshotXML(name, description), 0)
	return err
}

func (c *libvirtConn) ListSnapshots(ctx context.Context, domain string) ([]SnapshotInfo, error) {
	l, dom, err := c.lookup(ctx, domain)
	if err != nil {
		return nil, err
	}
	snaps, _, err := l.DomainListAllSnapshots(dom, 1, 0)
	if err != nil {
		return nil, err
	}
	currentName := ""
	if cur, err := l.DomainSnapshotCurrent(dom, 0); err == nil {
		currentName = cur.Name
	}
	out := make([]SnapshotInfo, 0, len(snaps))
	for _, s := range snaps {
		info := SnapshotInfo{Name: s.Name, Current: s.Name == currentName}
		if desc, err := l.DomainSnapshotGetXMLDesc(s, 0); err == nil {
			info.CreationTime, info.State = parseSnapshotXML(desc)
		}
		out = append(out, info)
	}
	return out, nil
}

func (c *libvirtConn) DeleteSnapshot(ctx context.Context, domain, name string) error {
	l, dom, err := c.lookup(ctx, domain)
	if err != nil {
		return err
	}
	snap, err := l.DomainSnapshotLookupByName(dom, name, 0)
	if err != nil {
		if isLibvirtErr(err, golibvirt.ErrNoDomainSnapshot) {
			return nil // already gone
		}
		return err
	}
	return l.DomainSnapshotDelete(snap, 0)
}

func (c *libvirtConn) RevertSnapshot(ctx context.Context, domain, name string) error {
	l, dom, err := c.lookup(ctx, domain)
	if err != nil {
		return err
	}
	snap, err := l.DomainSnapshotLookupByName(dom, name, 0)
	if err != nil {
		return err
	}
	return l.DomainRevertToSnapshot(snap, 0)
}

func buildSnapshotXML(name, description string) string {
	var b strings.Builder
	b.WriteString("<domainsnapshot>")
	fmt.Fprintf(&b, "<name>%s</name>", xmlEscape(name))
	if description != "" {
		fmt.Fprintf(&b, "<description>%s</description>", xmlEscape(description))
	}
	b.WriteString("</domainsnapshot>")
	return b.String()
}

func parseSnapshotXML(desc string) (time.Time, string) {
	var doc struct {
		State        string `xml:"state"`
		CreationTime int64  `xml:"creationTime"`
	}
	if err := xml.Unmarshal([]byte(desc), &doc); err != nil {
		return time.Time{}, ""
	}
	var t time.Time
	if doc.CreationTime > 0 {
		t = time.Unix(doc.CreationTime, 0).UTC()
	}
	return t, doc.State
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// parseVNCFromXML extracts the VNC listen host and TCP port from a live domain
// XML document. A port of -1 means VNC has not been allocated (domain not
// running).
func parseVNCFromXML(desc string) (string, int, error) {
	var doc struct {
		Devices struct {
			Graphics []struct {
				Type     string `xml:"type,attr"`
				Port     string `xml:"port,attr"`
				Listen   string `xml:"listen,attr"`
				ListenEl []struct {
					Address string `xml:"address,attr"`
				} `xml:"listen"`
			} `xml:"graphics"`
		} `xml:"devices"`
	}
	if err := xml.Unmarshal([]byte(desc), &doc); err != nil {
		return "", 0, fmt.Errorf("parse domain xml: %w", err)
	}
	for _, g := range doc.Devices.Graphics {
		if g.Type != "vnc" {
			continue
		}
		port, err := strconv.Atoi(strings.TrimSpace(g.Port))
		if err != nil || port <= 0 {
			return "", 0, fmt.Errorf("vnc port not allocated (domain not running)")
		}
		host := g.Listen
		if host == "" && len(g.ListenEl) > 0 {
			host = g.ListenEl[0].Address
		}
		if host == "" {
			host = "127.0.0.1"
		}
		return host, port, nil
	}
	return "", 0, fmt.Errorf("no vnc graphics device on domain")
}

func isLibvirtErr(err error, code golibvirt.ErrorNumber) bool {
	var e golibvirt.Error
	if errors.As(err, &e) {
		return e.Code == uint32(code)
	}
	return false
}
