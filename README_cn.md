# aria-tailscale

[English](README.md) | [中文](README_cn.md)

一个用 Go 实现的常驻服务，通过 [Tailscale Funnel](https://tailscale.com/kb/1223/funnel)
把内网 Ubuntu 上部署的服务暴露到公网，并按入站请求的 `Host` 头进行路由。

它基于 [`tailscale.com/tsnet`](https://pkg.go.dev/tailscale.com/tsnet)实现，
是一个**透明**的、基于 Host 的反向代理：对`app.example.com`、`api.example.com` 等外部 HTTPS 请求，
转发到对应的内网 HTTP 服务。Funnel 节点**不做任何鉴权**，仅按已知 Host 路由，未知 Host 返回 `404`，
内网服务自行负责自身鉴权。

## 工作原理

```
外部客户端 ──HTTPS（Host: app.example.com）──▶ Tailscale Funnel :443
                                                    │
                                                    ▼
                                            tsnet 节点（Go 二进制）
                                                    │  基于 Host 的路由表
                                                    ▼
                   ┌──────────────────────────────────────────────┐
                   │ app.example.com    → http://127.0.0.1:8080   │
                   │ api.example.com  → http://127.0.0.1:9090     │
                   │ 未知 Host               → 404                 │
                   └──────────────────────────────────────────────┘
```

Tailscale 会自动为 Funnel 节点签发 TLS 证书。要使用自定义域名
（`app.example.com`、`api.example.com`），需在 tailnet 管理后台把它们添加为
**Funnel 自定义域名**，并将其 DNS 指向该 Funnel 节点。代码侧只需按 `Host` 头路由，
无需处理证书。

## 构建

需要 Go 1.24+。

```bash
go mod tidy
go build -o aria-tailscale ./cmd/aria-tailscale
```

运行测试（无需 Tailscale）：

```bash
go test ./...
./scripts/test-funnel.sh      # local 模式冒烟测试（需要 curl + python3）
```

## 配置

路由通过 **YAML** 和/或 **CLI 参数** 配置。CLI 始终优先于 YAML（对某个 host 的
`--route` 会覆盖 YAML 中同一 host 的配置）。

### YAML（`--config path`）

```yaml
tailscale: funnel            # funnel | tailnet | ""（local）
listen: ":8787"
hostname: ariacompute-funnel
state_dir: /var/lib/aria-tailscale/tsnet
# auth_key: tskey-auth-...   # 建议用 TS_AUTHKEY 环境变量或 --auth-key

routes:
  - host: app.example.com
    upstream: http://127.0.0.1:8080
  - host: api.example.com
    upstream: http://127.0.0.1:9090
```

参见 `config/config.yaml`。

### CLI 参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `--config` | | YAML 配置文件路径 |
| `--listen` | `:8787` | 监听地址（local/funnel/tailnet 绑定） |
| `--tailscale` | `funnel` | `funnel`（公网 HTTPS）、`tailnet`（私有网格）或 `""`（local） |
| `--hostname` | `ariacompute-funnel` | tsnet 节点 hostname |
| `--state-dir` | `./.aria-tailscale-state` | tsnet 状态目录 |
| `--auth-key` | | Tailscale auth key（回退到 `TS_AUTHKEY` 环境变量）；funnel/tailnet 模式必填 |
| `--route` | | `host=upstream`，可重复；按 host 覆盖 YAML |

## 运行模式

**Funnel（公网 HTTPS）** —— 默认模式。加入 tailnet，在 `:443` 开启 Funnel，对外提供
配置的域名。当 `--listen :8787` 时会被重映射到 `:443`，并额外在 `:8787` 开启一个
本地明文 HTTP 监听，便于本地 curl 调试。

```bash
export TS_AUTHKEY=tskey-auth-...
./aria-tailscale --tailscale funnel --auth-key "$TS_AUTHKEY" \
  --route app.example.com=http://127.0.0.1:8080 \
  --route api.example.com=http://127.0.0.1:9090
```

查看公网地址：

```bash
curl -fsS -H "Host: app.example.com" https://<你的-funnel-主机>/
```

**Tailnet（仅私有网格）** —— 与 Funnel 相同，但不对外暴露：

```bash
./aria-tailscale --tailscale tailnet --auth-key "$TS_AUTHKEY" \
  --route app.example.com=http://127.0.0.1:8080
```

**Local（无 Tailscale，用于调试）** —— 绑定明文 HTTP 监听：

```bash
./aria-tailscale --tailscale '' --listen 127.0.0.1:8787 \
  --route local.ariacompute.test=http://127.0.0.1:8080
curl -H "Host: local.ariacompute.test" http://127.0.0.1:8787/
```

## Ubuntu 部署（Debian 包）

安装打包依赖：

```bash
sudo apt update
sudo apt install -y build-essential debhelper golang-1.24  # 或 golang-any
```

构建包（生成 `../aria-tailscale_<version>_amd64.deb`）：

```bash
./scripts/build-deb.sh
sudo dpkg -i ../aria-tailscale_<version>_amd64.deb
```

安装后，软件包会在 `/etc/aria-tailscale/config.yaml` 放置一份可用配置并
默认加载。编辑 `/etc/default/aria-tailscale` 设置 `TS_AUTHKEY`（或
`TS_AUTHKEY_FILE`），再编辑 `/etc/aria-tailscale/config.yaml` 添加你的路由
（或用 `ARIA_TAILSCALE_ROUTES` / `ARIA_TAILSCALE_CONFIG` 覆盖），然后：

```bash
sudo systemctl status aria-tailscale
sudo systemctl restart aria-tailscale
sudo journalctl -u aria-tailscale -f
```

服务的 tsnet 状态保存在 `/var/lib/aria-tailscale`，由
`/usr/lib/aria-tailscale/aria-tailscale-service` 启动脚本把
`/etc/default/aria-tailscale` 翻译为命令行参数后运行二进制。

### Tailscale 鉴权密钥

软件包不含任何 Tailscale 鉴权密钥。安装后任选其一提供：

- 在 `/etc/default/aria-tailscale` 中设置 `TS_AUTHKEY`（该文件权限为 `0600`）；
- 设置 `TS_AUTHKEY_FILE=/etc/aria-tailscale/ts_authkey`（指向一个 `0600`
  文件，推荐）；或
- 直接填写 `/etc/aria-tailscale/ts_authkey`，由 systemd 的 `LoadCredential`
  注入（可进一步用 `SetCredentialEncrypted` 做静态加密）。

密钥仅在该节点首次加入 tailnet 时需要；加入后其 machine key 会持久化在
`StateDir`，因此之后可删除该密钥。请勿通过命令行 `--auth-key` 传入，以免暴露在
`ps` / `/proc` 中。

## 发布

`.github/workflows/release.yml` 在打了 `aria-tailscale/vX.Y.Z` 标签的 GitHub
Release 上构建以下产物：

- `aria-tailscale_<v>_linux_amd64.tar.gz`
- `aria-tailscale_<v>_linux_arm64.tar.gz`
- `aria-tailscale_<v>_windows_x64.zip`
- `aria-tailscale_<v>_macos_universal.tar.gz`
- `aria-tailscale_<v>_amd64.deb`

## 说明

- 代理是透明的：它会自动设置 `X-Forwarded-For`，并补充 `X-Forwarded-Host`（原始域名）
  供内网服务识别，但不添加任何鉴权。
- 未知 Host 一律返回 `404`，避免 Funnel 节点把任意入站流量转发进内网。
- Funnel 需要一个**可复用**的 Tailscale auth key，请在 tailnet 管理后台按所需标签/ACL
  生成。

## 工程约定

本仓库遵循 Harness Engineering：

- [`AGENTS.md`](AGENTS.md)：Agent 工程上下文入口与目录索引
- [`requirements.md`](requirements.md)：需求规格（功能边界 / 异常 / 验收，人审后编码）
- [`task.md`](task.md)：实施清单
