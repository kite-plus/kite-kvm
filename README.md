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

## 部署

被控节点需运行在装有 `libvirtd` / KVM 的 Linux 宿主机上，并对 libvirt socket 有访问权限（通常加入 `libvirt` 组）。详见后续的 `deploy/` 与 `docs/api.md`。

## 状态

初版开发中，按功能点逐步提交。范围与路线图见项目计划。
