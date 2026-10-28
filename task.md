# Task list — aria-tailscale

Implement a Go service (module `ariacompute/tailscale`) that exposes internal
ariacompute services through Tailscale Funnel, routing by `Host` header.
Reference: `prombot-ai/gateway` (tsnet + `ListenFunnel`).

## Steps

- [x] Scaffold module: `go.mod` (Go 1.24.x, `tailscale.com` v1.104.0, cobra, yaml)
      and `cmd/aria-tailscale/main.go` (cobra root + `run`, signal shutdown).
- [x] `internal/funnel/config.go`: `Config`/`Route`, YAML load, `--route` parse,
      precedence merge (defaults → YAML → flags → TS_AUTHKEY → --route).
- [x] `internal/funnel/proxy.go`: Host-based `httputil` reverse proxy; 404 on
      unknown host; set `X-Forwarded-Host`.
- [x] `internal/funnel/server.go`: `Options`, `RegisterFlags`/`FlagsFromCmd`, `Run`,
      `runLocal`/`runTSNet` (funnel/tailnet), `logURL`, `withRecovery`.
- [x] `internal/funnel/` tests: `config_test.go` (LoadConfig/applyConfig/ParseRoute/
      defaultConfig), `proxy_test.go` (NewProxy errors, routing, 502, X-Forwarded-Host),
      `server_test.go` (FlagsFromCmd precedence/edge cases, withRecovery),
      `server_run_test.go` (Run local-mode integration). Coverage ~68%.
- [x] Deployment: `debian/` (control, rules, install, changelog, service, default,
      source/format), `debian/aria-tailscale-service` launcher,
      `config/config.yaml`, `scripts/build-deb.sh`,
      `scripts/test-funnel.sh`.
- [x] `.github/workflows/release.yml`: linux amd64/arm64, windows x64, macOS
      universal, Debian package on `aria-tailscale/vX.Y.Z` tags.
- [x] Docs: `README.md` (EN), `README_cn.md` (CN), `AGENTS.md`, `requirements.md`,
      `task.md`.

## Acceptance criteria

- `go build ./...` and `go test ./...` pass.
- `./scripts/test-funnel.sh` passes: known host routes (200), unknown host 404, in
  local mode.
- `./scripts/build-deb.sh` produces a `.deb` that installs `aria-tailscale`,
  the systemd unit, the launcher, and the default file.
- Funnel mode joins the tailnet and serves configured hosts over public HTTPS;
  local/tailnet modes work without a public endpoint.

## eMudhra / emSign 证书私钥泄露响应（2026-10）

背景：eMudhra(emSign) 8 张中间 CA 私钥极弱（P-384 私钥标量 <256），可被反推伪造任意域名证书；
Go 等自带信任库需升级。tailscale 是 Go 静态二进制（经 `tailscale.com` 客户端），以 Debian 打包
（`.deb`）部署到宿主，无 Dockerfile。

### 1. Go 二进制（决定性）
- `go.mod`：`go 1.27.1` → `go 1.27.3`，由 `crypto/x509` 硬编码封禁 8 张 emSign 中间证书。
  Tailscale 控制面与 Funnel 证书配置等出向 TLS 受该封禁保护，与宿主 `ca-certificates` 版本无关。

### 2. 宿主部署信任库
- `debian/control`：`Depends` 显式加入 `ca-certificates`，保证安装宿主具备系统信任库（Go 二进制
  出向 TLS 所需）。宿主仍须 `apt-get install ca-certificates` 保持最新（事件后发布）。

### 3. 检测与验证
- 新增 `scripts/check-emudhra.sh`：离线按 emSign 根（信任锚）指纹扫描宿主 CA bundle，规避代理假阳性。
