package provision

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kite-plus/kite-kvm/internal/libvirt"
)

func TestUserData(t *testing.T) {
	ci := CloudInit{
		InstanceID:  "vm1",
		Hostname:    "web1",
		DefaultUser: "ubuntu",
		Password:    "s3cret",
		SSHKeys:     []string{"ssh-ed25519 AAAAKEY user@host"},
	}
	ud := ci.userData()
	for _, want := range []string{
		"#cloud-config",
		`hostname: "web1"`,
		"ssh_pwauth: true",
		"  - name: ubuntu",
		"ssh_authorized_keys:",
		"ssh-ed25519 AAAAKEY user@host",
		"chpasswd:",
		"name: root, password:",
	} {
		if !strings.Contains(ud, want) {
			t.Errorf("user-data missing %q\n%s", want, ud)
		}
	}

	md := ci.metaData()
	if !strings.Contains(md, `instance-id: "vm1"`) || !strings.Contains(md, `local-hostname: "web1"`) {
		t.Errorf("meta-data wrong: %s", md)
	}
}

func TestUserDataNoPassword(t *testing.T) {
	ci := CloudInit{InstanceID: "vm1", Hostname: "h", DefaultUser: "ubuntu"}
	ud := ci.userData()
	if !strings.Contains(ud, "ssh_pwauth: false") {
		t.Error("expected ssh_pwauth false without password")
	}
	if strings.Contains(ud, "chpasswd") {
		t.Error("did not expect chpasswd without password")
	}
}

func TestNetworkConfigDHCP(t *testing.T) {
	nc := NetworkConfig{MAC: "52:54:00:aa:bb:01", Static: false}
	out := nc.render()
	if !strings.Contains(out, "dhcp4: true") {
		t.Errorf("expected dhcp4 in NAT config: %s", out)
	}
	if !strings.Contains(out, "macaddress: \"52:54:00:aa:bb:01\"") {
		t.Errorf("expected mac match: %s", out)
	}
}

func TestNetworkConfigStatic(t *testing.T) {
	nc := NetworkConfig{
		MAC:         "52:54:00:aa:bb:02",
		Static:      true,
		AddressCIDR: "203.0.113.10/24",
		Gateway:     "203.0.113.1",
		Nameservers: []string{"1.1.1.1", "8.8.8.8"},
	}
	out := nc.render()
	for _, want := range []string{
		`- "203.0.113.10/24"`,
		"to: default",
		`via: "203.0.113.1"`,
		`addresses: ["1.1.1.1", "8.8.8.8"]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("static net config missing %q\n%s", want, out)
		}
	}
}

func TestBuildSeedISO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seed.iso")
	files := []SeedFile{
		{Name: "meta-data", Content: []byte("instance-id: vm1\nlocal-hostname: h\n")},
		{Name: "user-data", Content: []byte("#cloud-config\nhostname: h\n")},
		{Name: "network-config", Content: []byte("version: 2\n")},
	}
	if err := BuildSeedISO(path, files); err != nil {
		t.Fatalf("BuildSeedISO: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("seed iso is empty")
	}
	// ISO9660 magic, the cidata volume label, and file contents must be present.
	for _, want := range []string{"CD001", "#cloud-config", "instance-id"} {
		if !bytes.Contains(data, []byte(want)) {
			t.Errorf("seed iso missing %q", want)
		}
	}
	if !bytes.Contains(bytes.ToLower(data), []byte("cidata")) {
		t.Error("seed iso missing cidata volume label")
	}
}

func TestProvisionerPrepare(t *testing.T) {
	dir := t.TempDir()
	conn := libvirt.NewFake()
	conn.BaseDir = dir
	p := NewProvisioner(conn, "default", dir)

	art, err := p.Prepare(context.Background(), PrepareRequest{
		ID:          "vm1",
		Hostname:    "web1",
		DefaultUser: "ubuntu",
		Password:    "pw",
		SSHKeys:     []string{"ssh-ed25519 AAAA"},
		BackingPath: "/base/jammy.img",
		DiskBytes:   20 << 30,
		Network:     NetworkConfig{MAC: "52:54:00:aa:bb:01"},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if !conn.HasVolume("default", "vm1.qcow2") {
		t.Error("overlay volume not created")
	}
	if art.DiskPath == "" {
		t.Error("empty disk path")
	}
	if _, err := os.Stat(art.SeedPath); err != nil {
		t.Errorf("seed iso not written: %v", err)
	}
}
