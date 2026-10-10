# Requirements — aria-tailscale

## Goal

Provide external access to internal Ubuntu-hosted ariacompute services (e.g.
`app.example.com`, `api.example.com`) through Tailscale Funnel, using a
Go implementation that mirrors the `prombot-ai/gateway` Funnel pattern.

## Functional requirements

1. **Tailscale Funnel ingress.** Join a tailnet as a tsnet node and expose the node
   on public HTTPS (port 443) via Funnel. Reuse the gateway's `tsnet.Server` +
   `ListenFunnel` approach.
2. **Host-based reverse proxy.** Route each incoming request to an internal upstream
   based on the `Host` header. Different external domains map to different internal
   services (e.g. `pin` → `:8080`, `agent` → `:9090`).
3. **Dual-channel configuration.** Support YAML config file and CLI flags. CLI has
   higher priority than YAML: a `--route` for a given host overrides the same host
   from YAML; other YAML routes remain.
4. **Transparent forwarding.** The Funnel node must not add authentication or alter
   payloads. It only forwards known hosts and returns `404` for unknown hosts.
5. **Three run modes.** `funnel` (public HTTPS), `tailnet` (private mesh only), and
   `local` (plain HTTP, no Tailscale — for debugging).
6. **Ubuntu packaging.** Ship a Go binary plus a systemd unit, an `/etc/default`
   template, and a debian package (`.deb`) produced via `dpkg-buildpackage`.
7. **CLI via cobra.** The `aria-tailscale` binary uses cobra with a `run`
   subcommand and standard flags.
8. **Latest Tailscale.** Build against the latest `tailscale.com` Go package
   (v1.104.0 at implementation time); keep Go toolchain aligned (1.24.x).
9. **Release pipeline.** A GitHub Actions workflow builds linux amd64 / linux arm64
   / windows x64 / macOS universal binaries and a Debian package on tagged releases.
10. **Unit tests.** Cover config loading/precedence, proxy routing (known host 200,
    unknown host 404, upstream failure 502), route parsing, panic recovery
    (`withRecovery`), and a local-mode `Run` integration test. Tailscale-dependent
    paths (`runTSNet`/`logURL`) are excluded as they require a live tailnet.

## Non-functional requirements

- Unknown hosts must always 404 to avoid forwarding arbitrary traffic into the
  internal network.
- Graceful shutdown on SIGINT/SIGTERM.
- The proxy must set `X-Forwarded-Host` (original domain) and rely on
  `X-Forwarded-For` for upstream services to identify the real client/origin.
- Funnel/tailnet modes require a reusable Tailscale auth key (`--auth-key` or
  `TS_AUTHKEY`); local mode does not.

## Out of scope

- Managing Tailscale custom-domain DNS or ACLs (operator task in the admin console).
- Authentication of internal services (delegated to those services).
- Service discovery or dynamic route registration at runtime.
