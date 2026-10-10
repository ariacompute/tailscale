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
      `etc/aria-tailscale.yaml.example`, `scripts/build-deb.sh`,
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
