# aria-tailscale

[English](README.md) | [中文](README_cn.md)

A Go service that exposes internal Ubuntu-hosted services to the
public internet through [Tailscale Funnel](https://tailscale.com/kb/1223/funnel),
routing by the incoming request `Host` header.

It is built on [`tailscale.com/tsnet`](https://pkg.go.dev/tailscale.com/tsnet)
and acts as a transparent, Host-based reverse proxy: external HTTPS requests for
`app.example.com` or `api.example.com` are forwarded to the corresponding internal
HTTP service.
The Funnel node performs **no authentication** — it only forwards known hosts and
returns `404` for anything else, so internal services remain responsible for their
own auth.

## How it works

```
External client ──HTTPS (Host: app.example.com)──▶ Tailscale Funnel :443
                                                      │
                                                      ▼
                                              tsnet node (Go binary)
                                                      │  Host-based route table
                                                      ▼
                       ┌──────────────────────────────────────────────┐
                       │ app.example.com   → http://127.0.0.1:8080    │
                       │ api.example.com → http://127.0.0.1:9090      │
                       │ unknown host           → 404                 │
                       └──────────────────────────────────────────────┘
```

Tailscale provisions TLS certificates for the Funnel node automatically. To use
custom domains (`app.example.com`, `api.example.com`), add them as
**Funnel custom domains** in your tailnet admin console and point their DNS at the
Funnel node. The Go side only routes on the `Host` header; no certificate handling
is needed in code.

## Build

Requires Go 1.24+.

```bash
go mod tidy
go build -o aria-tailscale ./cmd/aria-tailscale
```

Run the test suite (no Tailscale required):

```bash
go test ./...
./scripts/test-funnel.sh      # local-mode smoke test (needs curl + python3)
```

## Configuration

Routing is configured through **YAML** and/or **CLI flags**. CLI always wins over
YAML (a `--route` for a given host overrides the same host from YAML).

### YAML (`--config path`)

```yaml
tailscale: funnel            # funnel | tailnet | "" (local)
listen: ":8787"
hostname: ariacompute-funnel
state_dir: /var/lib/aria-tailscale/tsnet
# auth_key: tskey-auth-...   # prefer TS_AUTHKEY env or --auth-key

routes:
  - host: app.example.com
    upstream: http://127.0.0.1:8080
  - host: api.example.com
    upstream: http://127.0.0.1:9090
```

See `config/config.yaml`.

### CLI flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | | Path to YAML config file |
| `--listen` | `:8787` | Listen address (local/funnel/tailnet bind) |
| `--tailscale` | `funnel` | `funnel` (public HTTPS), `tailnet` (private mesh), or `""` (local) |
| `--hostname` | `ariacompute-funnel` | tsnet node hostname |
| `--state-dir` | `./.aria-tailscale-state` | tsnet state directory |
| `--auth-key` | | Tailscale auth key (fallback `TS_AUTHKEY` env); required for funnel/tailnet |
| `--route` | | `host=upstream`, repeatable; overrides YAML per host |

## Run modes

**Funnel (public HTTPS)** — the default. Joins the tailnet, opens Funnel on `:443`,
and serves the configured hosts publicly. When `--listen :8787`, it is remapped to
`:443` and an additional plain-HTTP listener is opened on `:8787` for local curl.

```bash
export TS_AUTHKEY=tskey-auth-...
./aria-tailscale --tailscale funnel --auth-key "$TS_AUTHKEY" \
  --route app.example.com=http://127.0.0.1:8080 \
  --route api.example.com=http://127.0.0.1:9090
```

Check the public name:

```bash
curl -fsS -H "Host: app.example.com" https://<your-funnel-host>/
```

**Tailnet (private mesh only)** — same as Funnel but not exposed publicly:

```bash
./aria-tailscale --tailscale tailnet --auth-key "$TS_AUTHKEY" \
  --route app.example.com=http://127.0.0.1:8080
```

**Local (no Tailscale, for debugging)** — binds a plain HTTP listener:

```bash
./aria-tailscale --tailscale '' --listen 127.0.0.1:8787 \
  --route local.ariacompute.test=http://127.0.0.1:8080
curl -H "Host: local.ariacompute.test" http://127.0.0.1:8787/
```

## Ubuntu deployment (Debian package)

Install packaging prerequisites:

```bash
sudo apt update
sudo apt install -y build-essential debhelper golang-1.24  # or golang-any
```

Build the package (produces `../aria-tailscale_<version>_amd64.deb`):

```bash
./scripts/build-deb.sh
sudo dpkg -i ../aria-tailscale_<version>_amd64.deb
```

After install, the package places a working config at
`/etc/aria-tailscale/config.yaml` and loads it automatically. Review
`/etc/default/aria-tailscale` and set `TS_AUTHKEY` (or `TS_AUTHKEY_FILE`),
then edit `/etc/aria-tailscale/config.yaml` to add your routes (or override
with `ARIA_TAILSCALE_ROUTES` / `ARIA_TAILSCALE_CONFIG`). Then:

```bash
sudo systemctl status aria-tailscale
sudo systemctl restart aria-tailscale
sudo journalctl -u aria-tailscale -f
```

The service stores tsnet state under `/var/lib/aria-tailscale` and runs the
binary via `/usr/lib/aria-tailscale/aria-tailscale-service`, which translates
`/etc/default/aria-tailscale` into flags.

### Tailscale auth key

The package never contains a Tailscale auth key. After install, choose one of:

- `TS_AUTHKEY` in `/etc/default/aria-tailscale` (that file is installed `0600`),
- `TS_AUTHKEY_FILE=/etc/aria-tailscale/ts_authkey` (a `0600` file; recommended), or
- fill `/etc/aria-tailscale/ts_authkey` and let systemd's `LoadCredential` feed
  it (optionally `SetCredentialEncrypted` for at-rest encryption).

The key is only needed the first time the node joins the tailnet; once joined,
its machine key persists in `StateDir`, so the auth key can be removed. Avoid
passing it on the command line (`--auth-key`) to keep it out of `ps`/`/proc`.

## Releases

`.github/workflows/release.yml` builds, on a GitHub Release tagged
`aria-tailscale/vX.Y.Z`, the following assets:

- `aria-tailscale_<v>_linux_amd64.tar.gz`
- `aria-tailscale_<v>_linux_arm64.tar.gz`
- `aria-tailscale_<v>_windows_x64.zip`
- `aria-tailscale_<v>_macos_universal.tar.gz`
- `aria-tailscale_<v>_amd64.deb`

## Notes

- The proxy is transparent: it sets `X-Forwarded-For` (automatically) and
  `X-Forwarded-Host` (original domain) for upstream services, but adds no auth.
- Unknown hosts always return `404`, preventing the Funnel node from forwarding
  arbitrary traffic into the internal network.
- Funnel requires a **reusable** Tailscale auth key. Generate one in the tailnet
  admin console with appropriate tags/ACLs.

## Engineering Conventions

This repository follows the Harness Engineering philosophy:

- [`AGENTS.md`](AGENTS.md): Agent engineering context entry and directory index
- [`requirements.md`](requirements.md): Requirements spec (feature boundaries/exceptions/acceptance criteria, human-review-gated)
- [`task.md`](task.md): Implementation task checklist
