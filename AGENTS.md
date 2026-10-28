# AGENTS.md — aria-tailscale

`aria-tailscale` is a Go service that exposes internal Ubuntu-hosted ariacompute
services to the public internet through Tailscale Funnel, routing by the incoming
request `Host` header. It is the ariacompute counterpart of the `prombot-ai/gateway`
Tailscale Funnel pattern, reimplemented as a transparent, Host-based reverse proxy.

## Repository layout

```
cmd/aria-tailscale/main.go   cobra root command + `run` subcommand; signal-based graceful shutdown
internal/funnel/
  config.go                    Config/Route types, YAML loading, --route parsing, precedence merge
  proxy.go                     Host-based reverse proxy (httputil); 404 for unknown hosts
  server.go                    Options, RegisterFlags/FlagsFromCmd, Run, runLocal/runTSNet, logURL
  server_test.go               FlagsFromCmd edge cases, withRecovery, Run local integration
  config_test.go               LoadConfig / applyConfig / ParseRoute / defaultConfig
  proxy_test.go                NewProxy errors, routing, 502, X-Forwarded-Host
config/config.yaml               example YAML config
contrib/ (via debian/)         aria-tailscale-service launcher translating /etc/default into flags
debian/                        debhelper packaging (control, rules, install, service, default)
scripts/
  build-deb.sh                 runs dpkg-buildpackage -us -uc -b
  test-funnel.sh               local-mode smoke test (curl + python3)
.github/workflows/release.yml  multi-platform release (linux amd64/arm64, windows x64, macos, deb)
```

## Architecture

- **tsnet** (`tailscale.com/tsnet`) joins the tailnet. `funnel` mode uses
  `srv.ListenFunnel("tcp", addr)`; `tailnet` uses `srv.Listen`; `local` uses
  `net.Listen`. The `:8787` default is remapped to `:443` under Funnel with an
  extra local `:8787` listener for debugging.
- **Routing** is a `map[string]*httputil.ReverseProxy` keyed by lower-cased host
  (port stripped). `Proxy.ServeHTTP` looks up `r.Host`; unknown hosts return 404.
- **Transparent forwarding**: no Basic Auth; `X-Forwarded-Host` is set to the
  original host; `X-Forwarded-For` is added automatically by `httputil`.
- **Config precedence**: built-in defaults → YAML file (`--config`) → CLI flags →
  `TS_AUTHKEY` env (auth key only) → `--route` (per-host override of YAML).

## Conventions

- Module path: `ariacompute/tailscale`. Go 1.27.3+. Key deps: `tailscale.com`
  (latest stable), `github.com/spf13/cobra`, `gopkg.in/yaml.v3`.
- Never modify the `prombot-ai/gateway` repo; this project only reuses its pattern.
- Keep the Funnel node transparent: do not add auth or content rewriting in the
  proxy. Unknown hosts must 404.
- Run `go test ./...` and `./scripts/test-funnel.sh` before publishing. Current
  `internal/funnel` unit-test coverage is ~68% (Tailscale-dependent `runTSNet`/
  `logURL` are excluded as they need a live tailnet).

## Trust stores (eMudhra/emSign CA key compromise, 2026-10)

- **Go 二进制**（静态，`CGO_ENABLED=0`，经 `tailscale.com` 客户端）：`go.mod` 升 `1.27.3`，由
  `crypto/x509` 硬编码封禁 8 张 emSign 中间证书。Tailscale 控制面与 Funnel 证书配置等出向 TLS 受该
  封禁保护——即便宿主系统 `ca-certificates` 较旧也会拒绝伪造链（决定性修复）。
- **宿主部署信任库**：本仓为 Debian 打包（`.deb`，`debian/` 目录），二进制安装到 Debian/Ubuntu 宿主，
  运行期信任库即宿主系统 `ca-certificates`。`debian/control` 的 `Depends` 已显式加入 `ca-certificates`
  以保证信任库存在；宿主仍须通过 `apt-get install ca-certificates` 保持最新（事件后发布）。
- `scripts/check-emudhra.sh`：离线按 emSign 根（信任锚）指纹扫描宿主 CA bundle（宿主通常含
  openssl/curl，可直接运行）。

## Common commands

```bash
go build -o aria-tailscale ./cmd/aria-tailscale
go test ./...
./scripts/test-funnel.sh
./scripts/build-deb.sh
```
