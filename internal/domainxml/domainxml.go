// Package domainxml renders a deterministic libvirt domain XML document from a
// high-level VM spec. The output uses virtio devices, host-passthrough CPU, a
// serial console for headless access, a placeholder VNC graphics device, and an
// optional NIC bandwidth cap.
package domainxml

import (
	"encoding/xml"
	"fmt"
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
	BandwidthMbps int // optional NIC rate cap, 0 = unlimited

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

	d := domain{
		Type:          "kvm",
		Name:          spec.Name,
		UUID:          spec.UUID,
		Memory:        memory{Unit: "MiB", Value: spec.MemoryMB},
		CurrentMemory: memory{Unit: "MiB", Value: spec.MemoryMB},
		VCPU:          vcpu{Placement: "static", Value: spec.VCPUs},
		OS:            osBlock{Type: osType{Arch: "x86_64", Machine: "q35", Value: "hvm"}, Boot: boot{Dev: "hd"}},
		Features:      features{ACPI: &empty{}, APIC: &empty{}},
		CPU:           cpu{Mode: "host-passthrough"},
		Clock:         clock{Offset: "utc"},
		OnPoweroff:    "destroy",
		OnReboot:      "restart",
		OnCrash:       "destroy",
		Devices:       buildDevices(spec, listen),
	}

	out, err := xml.MarshalIndent(d, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal domain xml: %w", err)
	}
	return string(out) + "\n", nil
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
	case spec.MAC == "":
		return fmt.Errorf("domainxml: mac is required")
	case spec.Network.Source == "":
		return fmt.Errorf("domainxml: network source is required")
	case spec.Network.Mode != ModeNAT && spec.Network.Mode != ModeBridge:
		return fmt.Errorf("domainxml: network mode must be %q or %q", ModeNAT, ModeBridge)
	}
	return nil
}

func buildDevices(spec Spec, listen string) devices {
	disks := []disk{{
		Type:   "file",
		Device: "disk",
		Driver: driver{Name: "qemu", Type: "qcow2"},
		Source: diskSource{File: spec.DiskPath},
		Target: diskTarget{Dev: "vda", Bus: "virtio"},
	}}
	if spec.SeedPath != "" {
		disks = append(disks, disk{
			Type:     "file",
			Device:   "cdrom",
			Driver:   driver{Name: "qemu", Type: "raw"},
			Source:   diskSource{File: spec.SeedPath},
			Target:   diskTarget{Dev: "sda", Bus: "sata"},
			ReadOnly: &empty{},
		})
	}

	iface := iface{
		MAC:   mac{Address: spec.MAC},
		Model: ifaceModel{Type: "virtio"},
	}
	switch spec.Network.Mode {
	case ModeBridge:
		iface.Type = "bridge"
		iface.Source = ifaceSource{Bridge: spec.Network.Source}
		if spec.Network.VLAN > 0 {
			iface.VLAN = &vlan{Tag: vlanTag{ID: spec.Network.VLAN}}
		}
	default: // NAT
		iface.Type = "network"
		iface.Source = ifaceSource{Network: spec.Network.Source}
	}
	if spec.BandwidthMbps > 0 {
		kbps := spec.BandwidthMbps * 125 // Mbit/s -> KB/s (libvirt's unit)
		iface.Bandwidth = &bandwidth{
			Inbound:  &bwParams{Average: kbps},
			Outbound: &bwParams{Average: kbps},
		}
	}

	return devices{
		Disks:     disks,
		Interface: iface,
		Serial:    serial{Type: "pty", Target: serialTarget{Port: 0}},
		Console:   console{Type: "pty", Target: consoleTarget{Type: "serial", Port: 0}},
		Graphics:  graphics{Type: "vnc", Port: -1, AutoPort: "yes", Listen: listen},
		Video:     video{Model: videoModel{Type: "vga"}},
		MemBalloon: memballoon{Model: "virtio"},
	}
}

// --- XML schema ------------------------------------------------------------

type domain struct {
	XMLName       xml.Name `xml:"domain"`
	Type          string   `xml:"type,attr"`
	Name          string   `xml:"name"`
	UUID          string   `xml:"uuid,omitempty"`
	Memory        memory   `xml:"memory"`
	CurrentMemory memory   `xml:"currentMemory"`
	VCPU          vcpu     `xml:"vcpu"`
	OS            osBlock  `xml:"os"`
	Features      features `xml:"features"`
	CPU           cpu      `xml:"cpu"`
	Clock         clock    `xml:"clock"`
	OnPoweroff    string   `xml:"on_poweroff"`
	OnReboot      string   `xml:"on_reboot"`
	OnCrash       string   `xml:"on_crash"`
	Devices       devices  `xml:"devices"`
}

type empty struct{}

type memory struct {
	Unit  string `xml:"unit,attr"`
	Value int    `xml:",chardata"`
}

type vcpu struct {
	Placement string `xml:"placement,attr,omitempty"`
	Value     int    `xml:",chardata"`
}

type osBlock struct {
	Type osType `xml:"type"`
	Boot boot   `xml:"boot"`
}

type osType struct {
	Arch    string `xml:"arch,attr"`
	Machine string `xml:"machine,attr,omitempty"`
	Value   string `xml:",chardata"`
}

type boot struct {
	Dev string `xml:"dev,attr"`
}

type features struct {
	ACPI *empty `xml:"acpi"`
	APIC *empty `xml:"apic"`
}

type cpu struct {
	Mode string `xml:"mode,attr"`
}

type clock struct {
	Offset string `xml:"offset,attr"`
}

type devices struct {
	Disks      []disk     `xml:"disk"`
	Interface  iface      `xml:"interface"`
	Serial     serial     `xml:"serial"`
	Console    console    `xml:"console"`
	Graphics   graphics   `xml:"graphics"`
	Video      video      `xml:"video"`
	MemBalloon memballoon `xml:"memballoon"`
}

type disk struct {
	Type     string     `xml:"type,attr"`
	Device   string     `xml:"device,attr"`
	Driver   driver     `xml:"driver"`
	Source   diskSource `xml:"source"`
	Target   diskTarget `xml:"target"`
	ReadOnly *empty     `xml:"readonly,omitempty"`
}

type driver struct {
	Name string `xml:"name,attr"`
	Type string `xml:"type,attr"`
}

type diskSource struct {
	File string `xml:"file,attr"`
}

type diskTarget struct {
	Dev string `xml:"dev,attr"`
	Bus string `xml:"bus,attr"`
}

type iface struct {
	Type      string      `xml:"type,attr"`
	MAC       mac         `xml:"mac"`
	Source    ifaceSource `xml:"source"`
	Model     ifaceModel  `xml:"model"`
	VLAN      *vlan       `xml:"vlan,omitempty"`
	Bandwidth *bandwidth  `xml:"bandwidth,omitempty"`
}

type mac struct {
	Address string `xml:"address,attr"`
}

type ifaceSource struct {
	Network string `xml:"network,attr,omitempty"`
	Bridge  string `xml:"bridge,attr,omitempty"`
}

type ifaceModel struct {
	Type string `xml:"type,attr"`
}

type vlan struct {
	Tag vlanTag `xml:"tag"`
}

type vlanTag struct {
	ID int `xml:"id,attr"`
}

type bandwidth struct {
	Inbound  *bwParams `xml:"inbound,omitempty"`
	Outbound *bwParams `xml:"outbound,omitempty"`
}

type bwParams struct {
	Average int `xml:"average,attr"`
	Peak    int `xml:"peak,attr,omitempty"`
	Burst   int `xml:"burst,attr,omitempty"`
}

type serial struct {
	Type   string       `xml:"type,attr"`
	Target serialTarget `xml:"target"`
}

type serialTarget struct {
	Port int `xml:"port,attr"`
}

type console struct {
	Type   string        `xml:"type,attr"`
	Target consoleTarget `xml:"target"`
}

type consoleTarget struct {
	Type string `xml:"type,attr"`
	Port int    `xml:"port,attr"`
}

type graphics struct {
	Type     string `xml:"type,attr"`
	Port     int    `xml:"port,attr"`
	AutoPort string `xml:"autoport,attr"`
	Listen   string `xml:"listen,attr"`
}

type video struct {
	Model videoModel `xml:"model"`
}

type videoModel struct {
	Type string `xml:"type,attr"`
}

type memballoon struct {
	Model string `xml:"model,attr"`
}
