// Package provision builds the bootable artifacts for a VM: a thin qcow2 overlay
// off a golden base image and a cloud-init NoCloud seed ISO carrying first-boot
// configuration (hostname, users, password, SSH keys, network).
package provision

import (
	"fmt"
	"strings"
)

// CloudInit holds the inputs for rendering the NoCloud seed documents.
type CloudInit struct {
	InstanceID  string // unique, stable per VM (== VM id)
	Hostname    string
	DefaultUser string // cloud image login (e.g. "ubuntu"); "" => root
	Password    string // optional plaintext password for root and the user
	SSHKeys     []string
	Network     NetworkConfig
}

// NetworkConfig describes the guest NIC for cloud-init network-config v2.
type NetworkConfig struct {
	MAC         string
	Static      bool     // true => static (bridge); false => DHCP (NAT)
	AddressCIDR string   // e.g. "203.0.113.10/24" (static only)
	Gateway     string   // static only
	Nameservers []string // static only
}

// Files returns the NoCloud documents to place on the seed ISO.
func (ci CloudInit) Files() []SeedFile {
	return []SeedFile{
		{Name: "meta-data", Content: []byte(ci.metaData())},
		{Name: "user-data", Content: []byte(ci.userData())},
		{Name: "network-config", Content: []byte(ci.Network.render())},
	}
}

func (ci CloudInit) metaData() string {
	return fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", ci.InstanceID, ci.Hostname)
}

func (ci CloudInit) userData() string {
	user := ci.DefaultUser
	if user == "" {
		user = "root"
	}

	var b strings.Builder
	b.WriteString("#cloud-config\n")
	fmt.Fprintf(&b, "hostname: %s\n", ci.Hostname)
	fmt.Fprintf(&b, "fqdn: %s\n", ci.Hostname)
	b.WriteString("manage_etc_hosts: true\n")
	b.WriteString("preserve_hostname: false\n")
	fmt.Fprintf(&b, "ssh_pwauth: %t\n", ci.Password != "")
	b.WriteString("disable_root: false\n")
	// Preserve host SSH keys when cloud-init re-runs after a password re-seed
	// (a new instance-id), so the host fingerprint does not churn.
	b.WriteString("ssh_deletekeys: false\n")

	b.WriteString("users:\n")
	fmt.Fprintf(&b, "  - name: %s\n", user)
	if user != "root" {
		b.WriteString("    sudo: \"ALL=(ALL) NOPASSWD:ALL\"\n")
		b.WriteString("    groups: [sudo]\n")
		b.WriteString("    shell: /bin/bash\n")
	}
	b.WriteString("    lock_passwd: false\n")
	if len(ci.SSHKeys) > 0 {
		b.WriteString("    ssh_authorized_keys:\n")
		for _, k := range ci.SSHKeys {
			fmt.Fprintf(&b, "      - %s\n", yamlQuote(k))
		}
	}

	if ci.Password != "" {
		b.WriteString("chpasswd:\n")
		b.WriteString("  expire: false\n")
		b.WriteString("  users:\n")
		fmt.Fprintf(&b, "    - {name: root, password: %s, type: text}\n", yamlQuote(ci.Password))
		if user != "root" {
			fmt.Fprintf(&b, "    - {name: %s, password: %s, type: text}\n", user, yamlQuote(ci.Password))
		}
	}

	return b.String()
}

func (nc NetworkConfig) render() string {
	var b strings.Builder
	b.WriteString("version: 2\n")
	b.WriteString("ethernets:\n")
	b.WriteString("  nic0:\n")
	if nc.MAC != "" {
		b.WriteString("    match:\n")
		fmt.Fprintf(&b, "      macaddress: %s\n", yamlQuote(nc.MAC))
		b.WriteString("    set-name: eth0\n")
	}
	if nc.Static {
		b.WriteString("    addresses:\n")
		fmt.Fprintf(&b, "      - %s\n", yamlQuote(nc.AddressCIDR))
		if nc.Gateway != "" {
			b.WriteString("    routes:\n")
			b.WriteString("      - to: default\n")
			fmt.Fprintf(&b, "        via: %s\n", yamlQuote(nc.Gateway))
		}
		if len(nc.Nameservers) > 0 {
			b.WriteString("    nameservers:\n")
			fmt.Fprintf(&b, "      addresses: [%s]\n", strings.Join(quoteAll(nc.Nameservers), ", "))
		}
	} else {
		b.WriteString("    dhcp4: true\n")
	}
	return b.String()
}

func yamlQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func quoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = yamlQuote(s)
	}
	return out
}
