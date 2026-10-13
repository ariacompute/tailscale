#!/usr/bin/env bash
# Smoke test for aria-tailscale in local mode (no Tailscale required).
#
# Starts one or two local upstream "echo" servers plus the proxy, then verifies:
#   - host-based routing to the correct upstream (including distinct upstreams)
#   - unknown host returns 404 (and a request with no matching Host also 404s)
#   - X-Forwarded-Host is set to the (lower-cased) incoming Host
#   - X-Forwarded-For is populated
#   - the request path + query string are forwarded unchanged
#   - routing works when configured via a YAML file (second proxy instance)
#
# Requirements: curl, python3, and a built aria-tailscale binary (built on demand).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${ROOT_DIR}/aria-tailscale"

# Build the binary if it is missing or not executable.
if [ ! -x "$BIN" ]; then
  echo "building aria-tailscale ..."
  (cd "$ROOT_DIR" && go build -o aria-tailscale ./cmd/aria-tailscale)
fi

# --- ports ---
UP1=18081
UP2=18082
PROXY1=18787   # proxy driven by CLI --route flags
PROXY2=18788   # proxy driven by a YAML config file

HOST_A="hosta.ariacompute.test"
HOST_B="hostb.ariacompute.test"

# --- upstream echo server (reports what it received) ---
UP_SRC="$(mktemp -t aria-up-XXXX.py)"
cat >"$UP_SRC" <<'PY'
import json, sys
from http.server import BaseHTTPRequestHandler, HTTPServer

class H(BaseHTTPRequestHandler):
    def _r(self):
        b = json.dumps({
            "upstream_port": self.server.server_address[1],
            "host": self.headers.get("Host"),
            "xfh": self.headers.get("X-Forwarded-Host"),
            "xff": self.headers.get("X-Forwarded-For"),
            "path": self.path,
            "method": self.command,
        }).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)
    do_GET = _r
    do_POST = _r
    do_PUT = _r
    do_DELETE = _r
    def log_message(self, *a):
        pass

HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
PY

python3 "$UP_SRC" "$UP1" >/dev/null 2>&1 &
UP1_PID=$!
python3 "$UP_SRC" "$UP2" >/dev/null 2>&1 &
UP2_PID=$!

# --- proxy instance 1: CLI routes ---
echo "starting proxy (CLI routes) on :${PROXY1} ..."
"$BIN" run --tailscale '' --listen "127.0.0.1:${PROXY1}" \
  --route "${HOST_A}=http://127.0.0.1:${UP1}" \
  --route "${HOST_B}=http://127.0.0.1:${UP2}" >/dev/null 2>&1 &
PROXY1_PID=$!

CFG="$(mktemp -t aria-cfg-XXXX.yaml)"
# Populated later; declared here so cleanup can remove it even on early exit.
cleanup() {
  for p in "${PROXY1_PID:-}" "${PROXY2_PID:-}" "${UP1_PID:-}" "${UP2_PID:-}"; do
    [ -n "$p" ] && kill "$p" 2>/dev/null || true
  done
  rm -f "$UP_SRC" "$CFG" 2>/dev/null || true
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

if ! wait_for "$PROXY1" "$HOST_A"; then
  echo "FAIL: proxy did not come up on :${PROXY1}"; exit 1
fi

pass=0; fail=0
# check <description> <0=pass,1=fail>
check() {
  if [ "$2" = "0" ]; then echo "OK   - $1"; pass=$((pass+1));
  else echo "FAIL - $1"; fail=$((fail+1)); fi
}

# 1) host A routes to its own upstream (UP1)
resp=$(curl -fsS -H "Host: ${HOST_A}" "http://127.0.0.1:${PROXY1}/")
echo "$resp" | grep -q "\"upstream_port\": ${UP1}" \
  && check "host A routes to upstream :${UP1}" 0 \
  || check "host A routes to upstream :${UP1} (resp=${resp})" 1

# 2) host B routes to a *different* upstream (UP2)
resp=$(curl -fsS -H "Host: ${HOST_B}" "http://127.0.0.1:${PROXY1}/")
echo "$resp" | grep -q "\"upstream_port\": ${UP2}" \
  && check "host B routes to distinct upstream :${UP2}" 0 \
  || check "host B routes to distinct upstream :${UP2} (resp=${resp})" 1

# 3) X-Forwarded-Host reflects the (lower-cased) incoming Host
echo "$resp" | grep -q "\"xfh\": \"${HOST_B}\"" \
  && check "X-Forwarded-Host = ${HOST_B}" 0 \
  || check "X-Forwarded-Host = ${HOST_B} (resp=${resp})" 1

# 4) X-Forwarded-For is populated by the reverse proxy
echo "$resp" | grep -Eq '"xff": "[^"]+' \
  && check "X-Forwarded-For is set" 0 \
  || check "X-Forwarded-For is set (resp=${resp})" 1

# 5) request path + query string are forwarded unchanged
resp=$(curl -fsS -H "Host: ${HOST_A}" "http://127.0.0.1:${PROXY1}/api/v1?q=1")
echo "$resp" | grep -q '"path": "/api/v1?q=1"' \
  && check "path/query forwarded unchanged" 0 \
  || check "path/query forwarded unchanged (resp=${resp})" 1

# 6) unknown host -> 404
code=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: unknown.example" "http://127.0.0.1:${PROXY1}/")
[ "$code" = "404" ] \
  && check "unknown host -> 404" 0 \
  || check "unknown host -> 404 (got ${code})" 1

# 7) request with no matching Host (default Host = IP) -> 404
code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:${PROXY1}/")
[ "$code" = "404" ] \
  && check "unmatched Host -> 404" 0 \
  || check "unmatched Host -> 404 (got ${code})" 1

# --- proxy instance 2: YAML config (explicit tailscale: "" means local) ---
cat >"$CFG" <<YAML
tailscale: ""
listen: "127.0.0.1:${PROXY2}"
hostname: ariacompute-funnel
routes:
  - host: ${HOST_A}
    upstream: http://127.0.0.1:${UP1}
  - host: ${HOST_B}
    upstream: http://127.0.0.1:${UP2}
YAML
# Routes come from the YAML file; the explicit empty `tailscale: ""` selects
# local mode (no Tailscale needed) without any CLI flag.
echo "starting proxy (YAML config routes) on :${PROXY2} ..."
"$BIN" run --config "$CFG" >/dev/null 2>&1 &
PROXY2_PID=$!

if ! wait_for "$PROXY2" "$HOST_A"; then
  echo "FAIL: proxy (config) did not come up on :${PROXY2}"; exit 1
fi

# 8) config-based routing reaches the right upstream
resp=$(curl -fsS -H "Host: ${HOST_A}" "http://127.0.0.1:${PROXY2}/")
echo "$resp" | grep -q "\"upstream_port\": ${UP1}" \
  && check "config: host A routes to :${UP1}" 0 \
  || check "config: host A routes to :${UP1} (resp=${resp})" 1

# 9) config: unknown host -> 404
code=$(curl -s -o /dev/null -w '%{http_code}' -H "Host: nope.example" "http://127.0.0.1:${PROXY2}/")
[ "$code" = "404" ] \
  && check "config: unknown host -> 404" 0 \
  || check "config: unknown host -> 404 (got ${code})" 1

echo "----"
echo "PASSED: ${pass}   FAILED: ${fail}"
if [ "$fail" = "0" ]; then
  echo "ALL SMOKE TESTS PASSED"
else
  echo "SOME TESTS FAILED"
  exit 1
fi
