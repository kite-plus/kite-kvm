package domainxml

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "update golden files")

func goldenCases() map[string]Spec {
	return map[string]Spec{
		"nat": {
			Name:     "kvm-vm1",
			UUID:     "11111111-1111-1111-1111-111111111111",
			VCPUs:    2,
			MemoryMB: 2048,
			DiskPath: "/var/lib/libvirt/images/vm1.qcow2",
			SeedPath: "/var/lib/libvirt/images/vm1-seed.iso",
			MAC:      "52:54:00:aa:bb:01",
			Network:  NetworkAttachment{Mode: ModeNAT, Source: "default"},
		},
		"bridge": {
			Name:          "kvm-vm2",
			UUID:          "22222222-2222-2222-2222-222222222222",
			VCPUs:         1,
			MemoryMB:      1024,
			DiskPath:      "/var/lib/libvirt/images/vm2.qcow2",
			SeedPath:      "/var/lib/libvirt/images/vm2-seed.iso",
			MAC:           "52:54:00:aa:bb:02",
			Network:       NetworkAttachment{Mode: ModeBridge, Source: "br0", VLAN: 100},
			BandwidthMbps: 200,
		},
	}
}

func TestRenderGolden(t *testing.T) {
	for name, spec := range goldenCases() {
		got, err := Render(spec)
		if err != nil {
			t.Fatalf("%s: Render: %v", name, err)
		}
		golden := filepath.Join("testdata", name+".xml")
		if *update {
			if err := os.MkdirAll("testdata", 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%s: read golden (run with -update): %v", name, err)
		}
		if got != string(want) {
			t.Errorf("%s: rendered XML does not match golden\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
		}
	}
}

func TestRenderContent(t *testing.T) {
	nat, err := Render(goldenCases()["nat"])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<cpu mode="host-passthrough">`,
		`<target dev="vda" bus="virtio">`,
		`device="cdrom"`,
		`<source network="default">`,
		`mac address="52:54:00:aa:bb:01"`,
		`<console type="pty">`,
		`<graphics type="vnc"`,
	} {
		if !strings.Contains(nat, want) {
			t.Errorf("nat XML missing %q", want)
		}
	}

	bridge, err := Render(goldenCases()["bridge"])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<source bridge="br0">`,
		`<tag id="100">`,
		`<bandwidth>`,
		`average="25000"`, // 200 Mbit/s -> 25000 KB/s
	} {
		if !strings.Contains(bridge, want) {
			t.Errorf("bridge XML missing %q", want)
		}
	}
}

func TestRenderValidation(t *testing.T) {
	bad := []Spec{
		{VCPUs: 1, MemoryMB: 512, DiskPath: "/d", MAC: "m", Network: NetworkAttachment{Mode: ModeNAT, Source: "default"}}, // no name
		{Name: "n", MemoryMB: 512, DiskPath: "/d", MAC: "m", Network: NetworkAttachment{Mode: ModeNAT, Source: "default"}}, // no vcpus
		{Name: "n", VCPUs: 1, MemoryMB: 512, DiskPath: "/d", MAC: "m", Network: NetworkAttachment{Mode: "routed", Source: "x"}}, // bad mode
	}
	for i, spec := range bad {
		if _, err := Render(spec); err == nil {
			t.Errorf("case %d: expected validation error", i)
		}
	}
}
