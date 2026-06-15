package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kite-kvm.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

const minimalConfig = `
server:
  insecure: true
auth:
  tokens: ["secret-token"]
networks:
  - id: nat-default
    mode: nat
    default: true
    libvirt_network: default
flavors:
  - id: s1.small
    name: Small
    vcpus: 1
    memory_mb: 1024
    disk_gb: 20
images:
  - id: ubuntu-22.04
    name: Ubuntu 22.04
    base_path: /var/lib/libvirt/images/base/jammy.img
`

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimalConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":8443" {
		t.Errorf("default addr = %q, want :8443", cfg.Server.Addr)
	}
	if cfg.Libvirt.URI != "qemu:///system" {
		t.Errorf("default uri = %q", cfg.Libvirt.URI)
	}
	if cfg.Libvirt.StoragePool != "default" {
		t.Errorf("default pool = %q", cfg.Libvirt.StoragePool)
	}
	if cfg.Storage.StatePath == "" {
		t.Error("state path default not applied")
	}
	if got := cfg.DefaultNetwork(); got == nil || got.ID != "nat-default" {
		t.Errorf("DefaultNetwork = %v", got)
	}
}

func TestLoadExampleConfig(t *testing.T) {
	cfg, err := Load("../../configs/kite-kvm.example.yaml")
	if err != nil {
		t.Fatalf("example config should be valid: %v", err)
	}
	if cfg.NetworkByID("public-1") == nil {
		t.Error("expected bridge network public-1")
	}
	if cfg.DefaultNetwork().ID != "nat-default" {
		t.Errorf("default network = %q, want nat-default", cfg.DefaultNetwork().ID)
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("KITE_AUTH_TOKENS", "a, b ,c")
	t.Setenv("KITE_SERVER_ADDR", ":9000")
	cfg, err := Load(writeConfig(t, minimalConfig))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.Addr != ":9000" {
		t.Errorf("addr override = %q", cfg.Server.Addr)
	}
	if len(cfg.Auth.Tokens) != 3 {
		t.Errorf("tokens override = %v", cfg.Auth.Tokens)
	}
}

func TestValidationErrors(t *testing.T) {
	cases := map[string]string{
		"no tokens": `
server: {insecure: true}
auth: {tokens: []}
networks: [{id: n, mode: nat, libvirt_network: default}]
flavors: [{id: f, name: F, vcpus: 1, memory_mb: 512, disk_gb: 10}]
images: [{id: i, name: I, base_path: /x.img}]
`,
		"tls required": `
auth: {tokens: ["t"]}
networks: [{id: n, mode: nat, libvirt_network: default}]
flavors: [{id: f, name: F, vcpus: 1, memory_mb: 512, disk_gb: 10}]
images: [{id: i, name: I, base_path: /x.img}]
`,
		"bad network mode": `
server: {insecure: true}
auth: {tokens: ["t"]}
networks: [{id: n, mode: routed}]
flavors: [{id: f, name: F, vcpus: 1, memory_mb: 512, disk_gb: 10}]
images: [{id: i, name: I, base_path: /x.img}]
`,
		"bridge without pool": `
server: {insecure: true}
auth: {tokens: ["t"]}
networks: [{id: n, mode: bridge, bridge: br0, gateway: 10.0.0.1}]
flavors: [{id: f, name: F, vcpus: 1, memory_mb: 512, disk_gb: 10}]
images: [{id: i, name: I, base_path: /x.img}]
`,
		"duplicate flavor": `
server: {insecure: true}
auth: {tokens: ["t"]}
networks: [{id: n, mode: nat, libvirt_network: default}]
flavors:
  - {id: f, name: F, vcpus: 1, memory_mb: 512, disk_gb: 10}
  - {id: f, name: F2, vcpus: 2, memory_mb: 512, disk_gb: 10}
images: [{id: i, name: I, base_path: /x.img}]
`,
		"no images": `
server: {insecure: true}
auth: {tokens: ["t"]}
networks: [{id: n, mode: nat, libvirt_network: default}]
flavors: [{id: f, name: F, vcpus: 1, memory_mb: 512, disk_gb: 10}]
images: []
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, body)); err == nil {
				t.Errorf("expected validation error for %q", name)
			}
		})
	}
}
