# kite-kvm

[English](README.md) | **简体中文**

一个单二进制的 **KVM 被控节点（control node）**：与 `libvirtd` 同机运行在 Linux 宿主机上，
通过 libvirt 管理本机虚拟机，对外暴露一套带鉴权的 REST API，供 WHMCS 这类财务系统或自建面板
**开通、计费、运维 VPS**。

## 设计要点

- **单二进制、无 cgo** —— 使用纯 Go 的 [`digitalocean/go-libvirt`](https://github.com/digitalocean/go-libvirt)
  （走 RPC over socket），`CGO_ENABLED=0 GOOS=linux` 即可从 macOS 直接交叉编译出单静态 Linux 二进制，
  无需 C 工具链。
- **同机部署** —— 通过本地 unix socket 连接 `qemu:///system`，不把 libvirtd 暴露到网络。
- **对账级健壮** —— 异步任务 + 幂等键 + SQLite 持久化，安全应对计费系统的重试与并发开通。
- **两种网络** —— NAT（端口转发）与桥接公网 IP，按 VM 选择。
- **随处可测** —— 所有 libvirt 调用收敛到一个 `Conn` 接口后，业务逻辑在 macOS 上即可用内存假实现单测。

## 功能

- 生命周期：创建、列表、详情、销毁（完整清理）。
- 电源：启动、优雅关机、重启、强制关机。
- 计费动作：挂起、解挂、重置密码。
- 重配：改主机名、按镜像重装、变更套餐（resize）。
- 实时资源统计（CPU / 内存 / 网络 / 磁盘）及区间速率。
- 开通：基于只读金镜像的薄 qcow2 差分盘 + cloud-init NoCloud seed（主机名、用户、密码、SSH key、
  网络），纯 Go 构建。
- 网络：NAT + 固定 DHCP 租约，或宿主网桥 + 公网 IP 池；可选每网卡带宽限速。
- 鉴权：TLS + Bearer Token + IP 白名单。

## 本地开发（macOS）

无 libvirt 时，把 `libvirt.uri` 设为 `fake://` 即可用内存假实现跑通全流程：

```yaml
# configs/dev.yaml
server: {addr: "127.0.0.1:8443", insecure: true}
auth: {tokens: ["devtoken"]}
libvirt: {uri: "fake://", instance_dir: "/tmp/kite-instances"}
storage: {state_path: "/tmp/kite.db"}
networks: [{id: nat-default, mode: nat, default: true, libvirt_network: default, subnet: "192.168.122.0/24"}]
flavors: [{id: s1.small, name: Small, vcpus: 1, memory_mb: 1024, disk_gb: 20}]
images:  [{id: ubuntu-22.04, name: Ubuntu 22.04, base_path: /tmp/base.img, default_user: ubuntu}]
```

```bash
go run ./cmd/kite-kvm -config configs/dev.yaml

curl -k -H "Authorization: Bearer devtoken" \
  -H "Idempotency-Key: $(uuidgen)" -H "Content-Type: application/json" \
  -d '{"flavor_id":"s1.small","image_id":"ubuntu-22.04","hostname":"web1"}' \
  https://127.0.0.1:8443/v1/vms
```

## 构建

需要 Go 1.25+。

```bash
make build        # 本机二进制 -> bin/kite-kvm
make build-linux  # 部署用的静态、无 cgo Linux 二进制
make test         # 单元测试
```

## 部署（Linux 宿主机）

被控节点运行在装有 `libvirtd` / KVM 的 Linux 宿主机上，并对 libvirt socket 有访问权限
（通常加入 `libvirt` 组）。

前置条件：

- 一个 libvirt 存储池（默认 `default`，目录型，位于 `/var/lib/libvirt/images`）。
- 只读金镜像放在 `libvirt.image_base_dir`（如 Ubuntu/Debian cloud img，自带 cloud-init 与 virtio）。
- NAT：默认 `default`/virbr0 网络；桥接：宿主预置网桥（如 `br0`）+ 公网 IP 池。
- 服务 TLS 证书与 Bearer Token。

```bash
make build-linux
sudo install bin/kite-kvm-linux-amd64 /usr/local/bin/kite-kvm
sudo install -D configs/kite-kvm.example.yaml /etc/kite-kvm/kite-kvm.yaml   # 按需修改
sudo install -D deploy/kite-kvm.service /etc/systemd/system/kite-kvm.service
sudo useradd -r -g libvirt kite-kvm
sudo systemctl enable --now kite-kvm
```

## API

完整参考见 [docs/api.md](docs/api.md)。所有端点都在 `/v1` 下，需 Bearer Token（并过 IP 白名单），
走 TLS。变更类操作为异步：返回 `202` + job，需 `Idempotency-Key`，通过 `GET /v1/jobs/{id}` 轮询。

## 路线图

已实现：VM 增删改查、电源操作、挂起/解挂、重置密码、改主机名、重装、变更套餐、实时统计、
NAT 与桥接公网 IP、异步任务 + 幂等键 + SQLite 持久化。

计划中：VNC/noVNC 控制台（令牌 + websocket 代理）、快照与备份、二级 IP / DNAT 端口映射、
Prometheus 指标、成品 WHMCS 模块、多宿主调度、LVM 存储池。
