// Command kite-kvm is a single-host KVM control node (被控节点). It manages the
// local hypervisor's virtual machines through libvirt and exposes a versioned,
// authenticated REST API so a billing system (WHMCS-style) or panel can
// provision and operate VPS instances.
package main

import (
	"flag"
	"fmt"
	"os"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	fmt.Fprintf(os.Stdout, "kite-kvm %s\n", version)
}
