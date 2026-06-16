# kite-kvm

[English](README.md) | **简体中文**

一个单二进制的 KVM 被控节点（control node）。与 `libvirtd` 同机运行在 Linux 宿主机上，
对外暴露一套带鉴权的 REST API，用于开通和运维 VPS。这套 API 与具体财务系统解耦——
WHMCS、IDCSmart 或自研面板各自在自己的仓库里对接它。

- **无 cgo** —— 纯 Go 的 [`go-libvirt`](https://github.com/digitalocean/go-libvirt) +
  [`libvirtxml`](https://libvirt.org/go/libvirtxml)，`CGO_ENABLED=0 GOOS=linux`
  可从 macOS 直接交叉编译出单静态二进制。
- **对账级健壮** —— 异步任务、幂等键、SQLite 持久化、启动自愈、有界重试，
  财务系统的重试与并发开通不会重复分配资源。
- **自包含** —— 通过本地 socket 连 `qemu:///system`；用 qcow2 差分盘 + cloud-init
  seed 开通；NAT 与桥接公网 IP 两种网络模式内置。
- **随处可测** —— 所有 libvirt 调用收敛到一个 `Conn` 接口后，整条链路可在 macOS 上
  用内存假实现跑通。

## 功能

VM 增删改查 · 电源操作 · 挂起/解挂 · 密码/主机名/重装/变更套餐 · 浏览器 VNC 控制台 ·
快照 · 按 VM 流量配额与超额自动断网 · 实时统计 · 主机容量与准入控制 ·
TLS + Bearer Token + IP 白名单。

## 环境要求

- 构建需 Go 1.25+。
- 运行需装有 `libvirtd` / KVM 的 Linux 宿主机（被控加入 `libvirt` 组）。

## 快速开始（无需 libvirt，任意系统）

把 `libvirt.uri` 设为 `fake://`，即可用内存假实现跑通全流程：

```yaml
# configs/dev.yaml
server:   {addr: "127.0.0.1:8443", insecure: true}
auth:     {tokens: ["devtoken"]}
libvirt:  {uri: "fake://", instance_dir: "/tmp/kite"}
storage:  {state_path: "/tmp/kite.db"}
networks: [{id: nat, mode: nat, default: true, libvirt_network: default}]
flavors:  [{id: s1.small, name: Small, vcpus: 1, memory_mb: 1024, disk_gb: 20}]
images:   [{id: ubuntu-22.04, name: Ubuntu, base_path: /tmp/base.img, default_user: ubuntu}]
```

```bash
go run ./cmd/kite-kvm -config configs/dev.yaml

curl -k -H "Authorization: Bearer devtoken" -H "Idempotency-Key: $(uuidgen)" \
  -d '{"flavor_id":"s1.small","image_id":"ubuntu-22.04","hostname":"web1"}' \
  https://127.0.0.1:8443/v1/vms
```

## 构建

```bash
make build         # 本机二进制 -> bin/kite-kvm
make build-linux   # 静态、无 cgo 的 Linux 二进制
make test
```

## 部署

```bash
make build-linux
sudo ./deploy/install.sh   # 二进制、配置、TLS 证书、systemd 单元、用户、宿主引导
```

`install.sh` 幂等。完整指南（TLS、桥接网络、备份、升级）见 [docs/deploy.md](docs/deploy.md)。

## 文档

- API：[docs/api.md](docs/api.md) · OpenAPI [docs/openapi.yaml](docs/openapi.yaml) · 渲染版 [docs/api.html](docs/api.html)
- 部署运维：[docs/deploy.md](docs/deploy.md)

所有 `/v1` 端点走 TLS 且需 Bearer Token。变更类操作为异步：返回 `202` + job，
需 `Idempotency-Key`，通过 `GET /v1/jobs/{id}` 轮询。

## 路线图

快照导出/备份到文件 · 二级 IP / DNAT 端口映射 · Prometheus 指标 · 多宿主调度 ·
LVM 存储池。
