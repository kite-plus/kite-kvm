# Networking setup

kite-kvm supports two per-VM network modes. `deploy/bootstrap-host.sh` sets up
the NAT one automatically; bridge mode needs a host bridge you create.

## NAT (`mode: nat`)

The libvirt `default` network (virbr0, `192.168.122.0/24`). VMs get a private
DHCP lease pinned by MAC; external reach is via port-forwarding. Defined from
[nat-default.xml](nat-default.xml) by the bootstrap script. Verify:

```bash
virsh -c qemu:///system net-list --all
```

## Bridge / public IP (`mode: bridge`)

VMs attach to a host bridge (e.g. `br0`) and get a real public IPv4 from the
configured `ip_pool` (written into the guest via cloud-init). You must create
the bridge so it carries the host's uplink. kite-kvm does **not** create it.

The `gateway`/`netmask` in the network config must match your upstream.

### netplan (Ubuntu)

`/etc/netplan/01-br0.yaml` — moves the uplink NIC into `br0`:

```yaml
network:
  version: 2
  ethernets:
    eno1:
      dhcp4: no
  bridges:
    br0:
      interfaces: [eno1]
      addresses: [203.0.113.2/24]   # the HOST's own IP
      routes:
        - to: default
          via: 203.0.113.1
      nameservers:
        addresses: [1.1.1.1, 8.8.8.8]
      parameters:
        stp: false
        forward-delay: 0
```

```bash
netplan apply
```

### NetworkManager (nmcli)

```bash
nmcli con add type bridge ifname br0 con-name br0
nmcli con add type bridge-slave ifname eno1 master br0
nmcli con mod br0 ipv4.addresses 203.0.113.2/24 ipv4.gateway 203.0.113.1 \
  ipv4.dns "1.1.1.1 8.8.8.8" ipv4.method manual
nmcli con up br0
```

> ⚠ Reconfiguring the primary NIC can drop your SSH session — do it on a console
> or with a rollback timer.

Then in `kite-kvm.yaml` set the bridge network's `bridge: br0` and an `ip_pool`
of spare public IPs (not the host's own).
