// Package domainxml renders deterministic libvirt domain XML from a high-level
// VM spec. It builds typed structs from libvirt.org/go/libvirtxml (pure Go, no
// cgo) rather than hand-writing XML, so the schema is validated at compile time.
// The output uses virtio devices, host-passthrough CPU, a serial console for
// headless access, a VNC graphics device, an optional NIC bandwidth cap, and an
// optional link-down state.
package domainxml

import (
	"fmt"

	"libvirt.org/go/libvirtxml"
)

// Network attachment modes.
const (
	ModeNAT    = "nat"
	ModeBridge = "bridge"
)

// Spec is the input to Render.
type Spec struct {
	Name     string // libvirt domain name, e.g. "kvm-<id>"
	UUID     string // optional; libvirt assigns one if empty
	VCPUs    int
	MemoryMB int
	DiskPath string // path to the per-VM qcow2 overlay (virtio root disk)
	SeedPath string // path to the cloud-init cidata seed ISO (cdrom); optional

	MAC           string
	Network       NetworkAttachment
	BandwidthMbps int  // optional NIC rate cap, 0 = unlimited
	LinkDown      bool // bring the NIC link down (network cut off)

	VNCListen string // VNC bind address; defaults to 127.0.0.1
}

// NetworkAttachment describes how the VM's NIC is connected.
type NetworkAttachment struct {
	Mode   string // "nat" | "bridge"
	Source string // nat: libvirt network name; bridge: host bridge device
	VLAN   int    // optional 802.1Q tag for bridge mode, 0 = untagged
}

// Render validates the spec and returns the libvirt domain XML.
func Render(spec Spec) (string, error) {
	if err := validate(spec); err != nil {
		return "", err
	}
	listen := spec.VNCListen
	if listen == "" {
		listen = "127.0.0.1"
	}

	dom := &libvirtxml.Domain{
		Type:          "kvm",
		Name:          spec.Name,
		UUID:          spec.UUID,
		Memory:        &libvirtxml.DomainMemory{Value: uint(spec.MemoryMB), Unit: "MiB"},
		CurrentMemory: &libvirtxml.DomainCurrentMemory{Value: uint(spec.MemoryMB), Unit: "MiB"},
		VCPU:          &libvirtxml.DomainVCPU{Placement: "static", Value: uint(spec.VCPUs)},
		OS: &libvirtxml.DomainOS{
			Type:        &libvirtxml.DomainOSType{Arch: "x86_64", Machine: "q35", Type: "hvm"},
			BootDevices: []libvirtxml.DomainBootDevice{{Dev: "hd"}},
		},
		Features: &libvirtxml.DomainFeatureList{
			ACPI: &libvirtxml.DomainFeature{},
			APIC: &libvirtxml.DomainFeatureAPIC{},
		},
		CPU:        &libvirtxml.DomainCPU{Mode: "host-passthrough"},
		Clock:      &libvirtxml.DomainClock{Offset: "utc"},
		OnPoweroff: "destroy",
		OnReboot:   "restart",
		OnCrash:    "destroy",
		Devices: &libvirtxml.DomainDeviceList{
			Disks:      buildDisks(spec),
			Interfaces: []libvirtxml.DomainInterface{buildInterface(spec)},
			Serials: []libvirtxml.DomainSerial{{
				Source: &libvirtxml.DomainChardevSource{Pty: &libvirtxml.DomainChardevSourcePty{}},
				Target: &libvirtxml.DomainSerialTarget{Port: uintPtr(0)},
			}},
			Consoles: []libvirtxml.DomainConsole{{
				Source: &libvirtxml.DomainChardevSource{Pty: &libvirtxml.DomainChardevSourcePty{}},
				Target: &libvirtxml.DomainConsoleTarget{Type: "serial", Port: uintPtr(0)},
			}},
			Graphics: []libvirtxml.DomainGraphic{{
				VNC: &libvirtxml.DomainGraphicVNC{Port: -1, AutoPort: "yes", Listen: listen},
			}},
			Videos:     []libvirtxml.DomainVideo{{Model: libvirtxml.DomainVideoModel{Type: "vga"}}},
			MemBalloon: &libvirtxml.DomainMemBalloon{Model: "virtio"},
		},
	}
	return dom.Marshal()
}

// RenderInterface renders just the <interface> element, for live device updates
// (e.g. cutting or restoring the NIC link via libvirt's update-device).
func RenderInterface(spec Spec) (string, error) {
	if err := validateInterface(spec); err != nil {
		return "", err
	}
	ifc := buildInterface(spec)
	return ifc.Marshal()
}

func buildDisks(spec Spec) []libvirtxml.DomainDisk {
	disks := []libvirtxml.DomainDisk{{
		Device: "disk",
		Driver: &libvirtxml.DomainDiskDriver{Name: "qemu", Type: "qcow2"},
		Source: &libvirtxml.DomainDiskSource{File: &libvirtxml.DomainDiskSourceFile{File: spec.DiskPath}},
		Target: &libvirtxml.DomainDiskTarget{Dev: "vda", Bus: "virtio"},
	}}
	if spec.SeedPath != "" {
		disks = append(disks, libvirtxml.DomainDisk{
			Device:   "cdrom",
			Driver:   &libvirtxml.DomainDiskDriver{Name: "qemu", Type: "raw"},
			Source:   &libvirtxml.DomainDiskSource{File: &libvirtxml.DomainDiskSourceFile{File: spec.SeedPath}},
			Target:   &libvirtxml.DomainDiskTarget{Dev: "sda", Bus: "sata"},
			ReadOnly: &libvirtxml.DomainDiskReadOnly{},
		})
	}
	return disks
}

// buildInterface renders the NIC, including an optional bandwidth cap and an
// optional link-down state (network cut off).
func buildInterface(spec Spec) libvirtxml.DomainInterface {
	ifc := libvirtxml.DomainInterface{
		MAC:    &libvirtxml.DomainInterfaceMAC{Address: spec.MAC},
		Model:  &libvirtxml.DomainInterfaceModel{Type: "virtio"},
		Source: &libvirtxml.DomainInterfaceSource{},
	}
	switch spec.Network.Mode {
	case ModeBridge:
		ifc.Source.Bridge = &libvirtxml.DomainInterfaceSourceBridge{Bridge: spec.Network.Source}
		if spec.Network.VLAN > 0 {
			ifc.VLan = &libvirtxml.DomainInterfaceVLan{
				Tags: []libvirtxml.DomainInterfaceVLanTag{{ID: uint(spec.Network.VLAN)}},
			}
		}
	default: // NAT
		ifc.Source.Network = &libvirtxml.DomainInterfaceSourceNetwork{Network: spec.Network.Source}
	}
	if spec.BandwidthMbps > 0 {
		kbps := spec.BandwidthMbps * 125 // Mbit/s -> KB/s (libvirt's unit)
		ifc.Bandwidth = &libvirtxml.DomainInterfaceBandwidth{
			Inbound:  &libvirtxml.DomainInterfaceBandwidthParams{Average: &kbps},
			Outbound: &libvirtxml.DomainInterfaceBandwidthParams{Average: &kbps},
		}
	}
	if spec.LinkDown {
		ifc.Link = &libvirtxml.DomainInterfaceLink{State: "down"}
	}
	return ifc
}

func validate(spec Spec) error {
	switch {
	case spec.Name == "":
		return fmt.Errorf("domainxml: name is required")
	case spec.VCPUs <= 0:
		return fmt.Errorf("domainxml: vcpus must be > 0")
	case spec.MemoryMB <= 0:
		return fmt.Errorf("domainxml: memory_mb must be > 0")
	case spec.DiskPath == "":
		return fmt.Errorf("domainxml: disk_path is required")
	}
	return validateInterface(spec)
}

func validateInterface(spec Spec) error {
	switch {
	case spec.MAC == "":
		return fmt.Errorf("domainxml: mac is required")
	case spec.Network.Source == "":
		return fmt.Errorf("domainxml: network source is required")
	case spec.Network.Mode != ModeNAT && spec.Network.Mode != ModeBridge:
		return fmt.Errorf("domainxml: network mode must be %q or %q", ModeNAT, ModeBridge)
	}
	return nil
}

func uintPtr(v uint) *uint { return &v }
