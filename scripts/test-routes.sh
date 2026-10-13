#!/usr/bin/env bash
# Route test for aria-tailscale in local mode (no Tailscale required).
#
# Starts the Python upstream echo server (test/upstream.py) on 127.0.0.1:8080
# (and :8081 for a second, distinct route), then starts the proxy in local mode
# with the routes from config/config.yaml and verifies:
#   - Host-based routing reaches the correct upstream (incl. distinct upstreams)
#   - unknown Host returns 404 (and a request with no matching Host also 404s)
#   - X-Forwarded-Host is set to the (lower-cased) incoming Host
#   - X-Forwarded-For is populated
#   - the request path + query string are forwarded unchanged
#
# Requirements: curl, python3, and a built aria-tailscale binary (built on demand).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${ROOT_DIR}/aria-tailscale"
UP_SRC="${ROOT_DIR}/test/upstream.py"

# Build the binary if it is missing or not executable.
if [ ! -x "$BIN" ]; then
  echo "building aria-tailscale ..."
  (cd "$ROOT_DIR" && go build -o aria-tailscale ./cmd/aria-tailscale)
fi

# --- ports ---
UP1=8080    # matches the upstream in config/config.yaml
UP2=8081
PROXY=18787 # proxy driven by the local-mode config

HOST1="test.ariacompute.com"   # the route defined in config/config.yaml
HOST2="api.ariacompute.test"   # a second route to prove distinct upstream routing

# --- upstream echo servers (report what they received) ---
python3 "$UP_SRC" "$UP1" >/dev/null 2>&1 &
UP1_PID=$!
python3 "$UP_SRC" "$UP2" >/dev/null 2>&1 &
UP2_PID=$!

# --- proxy: local-mode config mirroring config/config.yaml's routes ---
CFG="$(mktemp -t aria-routes-XXXX.yaml)"
cat >"$CFG" <<YAML
tailscale: ""
listen: "127.0.0.1:${PROXY}"
hostname: ariacompute-funnel
routes:
  - host: ${HOST1}
    upstream: http://127.0.0.1:${UP1}
  - host: ${HOST2}
    upstream: http://127.0.0.1:${UP2}
YAML

cleanup() {
  for p in "${PROXY_PID:-}" "${UP1_PID:-}" "${UP2_PID:-}"; do
    [ -n "$p" ] && kill "$p" 2>/dev/null || true
  done
  rm -f "$CFG" 2>/dev/null || true
}
trap cleanup EXIT

# Wait until the proxy answers for the given Host (returns 0/1).
wait_for() {
  local port="$1" host="$2"
  for _ in $(seq 1 50); do
    if curl -fsS -o /dev/null -H "Host: $host" "http://127.0.0.1:${port}/" 2>/dev/null; then
      return 0
    fi
    sleep 0.2
  done
  return 1
}

echo "starting proxy (local-mode routes from config) on :${PROXY} ..."
"$BIN" run --config "$CFG" >/dev/null 2>&1 &
PROXY_PID=$!

if ! wait_for "$PROXY" "$HOST1"; then
  echo "FAIL: proxy did not come up on :${PROXY}"; exit 1
fi

pass=0; fail=0
# check <description> <0=pass,1=fail>
check() {
  if [ "$2" = "0" ]; then echo "OK   - $1"; pass=$((pass+1));
  else echo "FAIL - $1"; fail=$((fail+1)); fi
}

# 1) HOST1 routes to its own upstream (UP1 = 8080)
resp=$(curl -fsS -H "Host: ${HOST1}" "http://127.0.0.1:${PROXY}/")
echo "$resp" | grep -q "\"upstream_port\": ${UP1}" \
  && check "host ${HOST1} routes to upstream :${UP1}" 0 \
  || check "host ${HOST1} routes to upstream :${UP1} (resp=${resp})" 1

# 2) HOST2 routes to a *different* upstream (UP2 = 8081)
resp=$(curl -fsS -H "Host: ${HOST2}" "http://127.0.0.1:${PROXY}/")
echo "$resp" | grep -q "\"upstream_port\": ${UP2}" \
  && check "host ${HOST2} routes to distinct upstream :${UP2}" 0 \
  || check "host ${HOST2} routes to distinct upstream :${UP2} (resp=${resp})" 1

# 3) X-Forwarded-Host reflects the (lower-cased) incoming Host
echo "$resp" | grep -q "\"xfh\": \"${HOST2}\"" \
  && check "X-Forwarded-Host = ${HOST2}" 0 \
  || check "X-Forwarded-Host = ${HOST2} (resp=${resp})" 1

# 4) X-Forwarded-For is populated by the reverse proxy
echo "$resp" | grep -Eq '"xff": "[^"]+' \
  && check "X-Forwarded-For is set" 0 \
  || check "X-Forwarded-For is set (resp=${resp})" 1

# 5) request path + query string are forwarded unchanged
resp=$(curl -fsS -H "Host: ${HOST1}" "http://127.0.0.1:${PROXY}/api/v1?q=1")
echo "$resp" | grep -q '"path": "/api/v1?q=1"' \
  && check "path/query forwarded unchanged" 0 \
  || check "path/query forwarded unchanged (resp=${resp})" 1

# 6) unknown host -> 404
code=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: unknown.example" "http://127.0.0.1:${PROXY}/")
[ "$code" = "404" ] \
  && check "unknown host -> 404" 0 \
  || check "unknown host -> 404 (got ${code})" 1

# 7) request with no matching Host (default Host = IP) -> 404
code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${PROXY}/")
[ "$code" = "404" ] \
  && check "unmatched Host -> 404" 0 \
  || check "unmatched Host -> 404 (got ${code})" 1

echo "----"
echo "PASSED: ${pass}   FAILED: ${fail}"
if [ "$fail" = "0" ]; then
  echo "ALL ROUTE TESTS PASSED"
else
  echo "SOME TESTS FAILED"
  exit 1
fi
