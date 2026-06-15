package libvirt

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Fake is an in-memory Conn implementation for development and tests on hosts
// without libvirt (e.g. macOS). It models domain lifecycle, storage volumes,
// and NAT DHCP leases so the full provisioning and lifecycle logic can be
// exercised without a hypervisor.
type Fake struct {
	mu sync.Mutex

	// PingErr, when set, makes Ping fail (useful for readiness tests).
	PingErr error
	// BaseDir is the synthetic storage pool target path used to build volume
	// paths returned by CreateVolume.
	BaseDir string

	domains   map[string]*fakeDomain
	volumes   map[string]string              // "pool/name" -> path
	dhcp      map[string]map[string]dhcpHost // network -> mac -> host
	stats     map[string]DomainStats         // optional injected per-domain stats
	snapshots map[string][]SnapshotInfo      // domain -> snapshots
}

type fakeDomain struct {
	uuid  string
	xml   string
	state DomainState
}

type dhcpHost struct {
	name string
	ip   string
}

// NewFake returns a ready in-memory Conn.
func NewFake() *Fake {
	return &Fake{
		BaseDir:   "/var/lib/libvirt/images",
		domains:   map[string]*fakeDomain{},
		volumes:   map[string]string{},
		dhcp:      map[string]map[string]dhcpHost{},
		stats:     map[string]DomainStats{},
		snapshots: map[string][]SnapshotInfo{},
	}
}

func (f *Fake) Connect(context.Context) error { return nil }
func (f *Fake) Close() error                  { return nil }

func (f *Fake) Ping(context.Context) error { return f.PingErr }

func (f *Fake) DefineDomain(_ context.Context, xml string) (string, error) {
	name, err := domainNameFromXML(xml)
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.domains[name]
	if !ok {
		d = &fakeDomain{uuid: "fake-uuid-" + name, state: StateShutoff}
		f.domains[name] = d
	}
	d.xml = xml
	return d.uuid, nil
}

func (f *Fake) StartDomain(_ context.Context, name string) error {
	return f.setState(name, StateRunning)
}

func (f *Fake) ShutdownDomain(_ context.Context, name string) error {
	return f.setState(name, StateShutoff)
}

func (f *Fake) RebootDomain(_ context.Context, name string) error {
	return f.requireDomain(name, func(*fakeDomain) { /* stays running */ })
}

func (f *Fake) DestroyDomain(_ context.Context, name string) error {
	return f.setState(name, StateShutoff)
}

func (f *Fake) SuspendDomain(_ context.Context, name string) error {
	return f.setState(name, StatePaused)
}

func (f *Fake) ResumeDomain(_ context.Context, name string) error {
	return f.setState(name, StateRunning)
}

func (f *Fake) UndefineDomain(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.domains[name]; !ok {
		return ErrDomainNotFound
	}
	delete(f.domains, name)
	return nil
}

func (f *Fake) DomainState(_ context.Context, name string) (DomainState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.domains[name]
	if !ok {
		return StateUnknown, ErrDomainNotFound
	}
	return d.state, nil
}

func (f *Fake) ListDomains(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	names := make([]string, 0, len(f.domains))
	for n := range f.domains {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, nil
}

func (f *Fake) DomainXML(_ context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.domains[name]
	if !ok {
		return "", ErrDomainNotFound
	}
	return d.xml, nil
}

func (f *Fake) DomainVNCAddress(_ context.Context, name string) (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.domains[name]; !ok {
		return "", 0, ErrDomainNotFound
	}
	return "127.0.0.1", 5901, nil
}

func (f *Fake) CreateVolume(_ context.Context, spec StorageVolSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := spec.Pool + "/" + spec.Name
	path := filepath.Join(f.BaseDir, spec.Name)
	f.volumes[key] = path
	return path, nil
}

func (f *Fake) DeleteVolume(_ context.Context, pool, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.volumes, pool+"/"+name)
	return nil
}

func (f *Fake) ResizeVolume(_ context.Context, pool, name string, _ uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.volumes[pool+"/"+name]; !ok {
		return fmt.Errorf("libvirt fake: volume %s/%s not found", pool, name)
	}
	return nil
}

func (f *Fake) AllDomainStats(context.Context) ([]DomainStats, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]DomainStats, 0, len(f.domains))
	for name, d := range f.domains {
		if s, ok := f.stats[name]; ok {
			s.Name = name
			s.State = d.state
			out = append(out, s)
			continue
		}
		out = append(out, DomainStats{Name: name, State: d.state})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *Fake) AddDHCPHost(_ context.Context, network, mac, name, ip string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dhcp[network] == nil {
		f.dhcp[network] = map[string]dhcpHost{}
	}
	f.dhcp[network][mac] = dhcpHost{name: name, ip: ip}
	return nil
}

func (f *Fake) RemoveDHCPHost(_ context.Context, network, mac string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if hosts := f.dhcp[network]; hosts != nil {
		delete(hosts, mac)
	}
	return nil
}

func (f *Fake) CreateSnapshot(_ context.Context, domain, name, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.domains[domain]
	if !ok {
		return ErrDomainNotFound
	}
	for i := range f.snapshots[domain] {
		f.snapshots[domain][i].Current = false
	}
	f.snapshots[domain] = append(f.snapshots[domain], SnapshotInfo{
		Name:         name,
		State:        d.state.String(),
		CreationTime: time.Now().UTC(),
		Current:      true,
	})
	return nil
}

func (f *Fake) ListSnapshots(_ context.Context, domain string) ([]SnapshotInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.domains[domain]; !ok {
		return nil, ErrDomainNotFound
	}
	out := make([]SnapshotInfo, len(f.snapshots[domain]))
	copy(out, f.snapshots[domain])
	return out, nil
}

func (f *Fake) DeleteSnapshot(_ context.Context, domain, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	snaps := f.snapshots[domain]
	kept := snaps[:0]
	for _, s := range snaps {
		if s.Name != name {
			kept = append(kept, s)
		}
	}
	f.snapshots[domain] = kept
	return nil
}

func (f *Fake) RevertSnapshot(_ context.Context, domain, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.snapshots[domain] {
		if s.Name == name {
			return nil
		}
	}
	return fmt.Errorf("libvirt fake: snapshot %q not found on %q", name, domain)
}

// --- test inspection helpers -----------------------------------------------

// HasDomain reports whether a domain is currently defined.
func (f *Fake) HasDomain(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.domains[name]
	return ok
}

// HasVolume reports whether a volume exists in the pool.
func (f *Fake) HasVolume(pool, name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.volumes[pool+"/"+name]
	return ok
}

// DHCPHostIP returns the leased IP for a MAC on a network, or "".
func (f *Fake) DHCPHostIP(network, mac string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if hosts := f.dhcp[network]; hosts != nil {
		return hosts[mac].ip
	}
	return ""
}

// SetStats injects per-domain stats returned by AllDomainStats.
func (f *Fake) SetStats(name string, s DomainStats) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stats[name] = s
}

func (f *Fake) setState(name string, state DomainState) error {
	return f.requireDomain(name, func(d *fakeDomain) { d.state = state })
}

func (f *Fake) requireDomain(name string, fn func(*fakeDomain)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.domains[name]
	if !ok {
		return ErrDomainNotFound
	}
	fn(d)
	return nil
}

// domainNameFromXML extracts <name>...</name> from a domain XML document. It is
// intentionally minimal — the fake only needs the domain name as a key.
func domainNameFromXML(xml string) (string, error) {
	const openTag, closeTag = "<name>", "</name>"
	i := strings.Index(xml, openTag)
	if i < 0 {
		return "", fmt.Errorf("libvirt fake: domain xml missing <name>")
	}
	rest := xml[i+len(openTag):]
	j := strings.Index(rest, closeTag)
	if j < 0 {
		return "", fmt.Errorf("libvirt fake: domain xml missing </name>")
	}
	return rest[:j], nil
}
