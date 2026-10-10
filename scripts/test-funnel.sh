#!/usr/bin/env bash
# Smoke test for aria-tailscale in local mode (no Tailscale required):
# starts a local upstream + the proxy, then verifies routing and 404.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${ROOT_DIR}/aria-tailscale"

if [ ! -x "$BIN" ]; then
  echo "building aria-tailscale ..."
  (cd "$ROOT_DIR" && go build -o aria-tailscale ./cmd/aria-tailscale)
fi

UP_PORT=18080
PROXY_PORT=18787

UP_DIR="$(mktemp -d)"
python3 -m http.server "$UP_PORT" --directory "$UP_DIR" >/dev/null 2>&1 &
UP_PID=$!
trap 'kill "$UP_PID" 2>/dev/null || true' EXIT

"$BIN" run --tailscale '' --listen "127.0.0.1:${PROXY_PORT}" \
  --route "local.ariacompute.test=http://127.0.0.1:${UP_PORT}" >/dev/null 2>&1 &
PROXY_PID=$!
trap 'kill "$PROXY_PID" 2>/dev/null || true; kill "$UP_PID" 2>/dev/null || true' EXIT

# Wait for the proxy to come up.
for _ in $(seq 1 20); do
  if curl -fsS -o /dev/null "http://127.0.0.1:${PROXY_PORT}/" 2>/dev/null; then
    break
  fi
  sleep 0.2
done

echo "== known host (expect 200) =="
if curl -fsS -H "Host: local.ariacompute.test" "http://127.0.0.1:${PROXY_PORT}/" >/dev/null; then
  echo "OK"
else
  echo "FAIL: known host not routed"; exit 1
fi

echo "== unknown host (expect 404) =="
code=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: evil.example" "http://127.0.0.1:${PROXY_PORT}/")
if [ "$code" != "404" ]; then
  echo "FAIL: unknown host returned $code"; exit 1
fi
echo "OK (404)"

echo "ALL SMOKE TESTS PASSED"
