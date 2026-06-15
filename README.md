# kite-kvm

一个运行在 Linux KVM 宿主机上的 **被控节点（control node / agent）**：通过 libvirt 管理本机虚拟机，对外暴露一套标准化、带鉴权的 REST API，供 WHMCS 这类财务系统或自建面板**开通、计费、运维 VPS**。

> A single-host KVM control node written in Go. It wraps `libvirt` and exposes a
> versioned REST API for provisioning and operating VPS instances.

## 设计要点

- **单二进制、无 cgo**：使用纯 Go 的 [`digitalocean/go-libvirt`](https://github.com/digitalocean/go-libvirt) 走 RPC over socket，`CGO_ENABLED=0 GOOS=linux` 即可从 macOS 直接交叉编译出单静态 Linux 二进制。
- **同机部署**：通过本地 unix socket 连接 `qemu:///system`，不把 libvirtd 暴露到网络。
- **对账级健壮**：异步任务 + 幂等键 + SQLite 持久化，安全应对计费系统的重试与并发开通。
- **两种网络**：NAT（端口转发）与桥接公网 IP，按 VM 选择。
- **可测**：所有 libvirt 调用收敛到一个 `Conn` 接口后，业务逻辑在 macOS 上即可单测（内存 fake）。

## 开发

需要 Go 1.25+。

```bash
make build        # 构建本机二进制 (bin/kite-kvm)
make build-linux  # 交叉编译出部署用的静态 Linux 二进制
make test         # 运行单元测试
make run          # 本地运行
```

## 本地运行（macOS 开发）

无 libvirt 时，把 `libvirt.uri` 设为 `fake://` 即可用内存假实现跑通全流程：

```yaml
server: {addr: "127.0.0.1:8443", insecure: true}
auth: {tokens: ["devtoken"]}
libvirt: {uri: "fake://", instance_dir: "/tmp/kite-instances"}
storage: {state_path: "/tmp/kite.db"}
networks: [{id: nat-default, mode: nat, default: true, libvirt_network: default, subnet: "192.168.122.0/24"}]
flavors: [{id: s1.small, name: Small, vcpus: 1, memory_mb: 1024, disk_gb: 20}]
images:  [{id: ubuntu-22.04, name: Ubuntu 22.04, base_path: /tmp/base.img, default_user: ubuntu}]
```

```bash
make run                # 或 ./bin/kite-kvm -config configs/dev.yaml
curl -k -H "Authorization: Bearer devtoken" \
  -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
  -d '{"flavor_id":"s1.small","image_id":"ubuntu-22.04","hostname":"web1"}' \
  https://127.0.0.1:8443/v1/vms
```

## 部署（Linux 宿主机）

被控节点运行在装有 `libvirtd` / KVM 的 Linux 宿主机上，并对 libvirt socket 有访问权限（通常加入 `libvirt` 组）。

前置条件：
- 一个 libvirt 存储池（默认 `default`，目录型，位于 `/var/lib/libvirt/images`）。
- 只读金镜像放在 `libvirt.image_base_dir`（如 Ubuntu/Debian cloud img，自带 cloud-init 与 virtio）。
- NAT：默认 `default`/virbr0 网络；桥接：宿主预置网桥（如 `br0`）+ 公网 IP 池。
- 服务 TLS 证书与 Bearer Token。

步骤：

```bash
make build-linux                         # 交叉编译出静态二进制
sudo install bin/kite-kvm-linux-amd64 /usr/local/bin/kite-kvm
sudo install -D configs/kite-kvm.example.yaml /etc/kite-kvm/kite-kvm.yaml   # 按需修改
sudo install -D deploy/kite-kvm.service /etc/systemd/system/kite-kvm.service
sudo useradd -r -g libvirt kite-kvm
sudo systemctl enable --now kite-kvm
```

API 参考见 [docs/api.md](docs/api.md)。

## 状态

初版已实现：VM 增删改查、电源操作、suspend/unsuspend、改密码、资源统计、
NAT 与桥接公网 IP、异步任务 + 幂等键 + SQLite 持久化。VNC 控制台等见路线图。
