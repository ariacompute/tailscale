#!/usr/bin/env python3
"""Minimal upstream echo server for aria-tailscale route tests.

Listens on 127.0.0.1. The port can be given as the first CLI argument
(default 8080, matching the upstream in config/config.yaml). For every request
it returns a JSON body describing what the reverse proxy forwarded — Host,
path, method and the X-Forwarded-* headers — so a test can assert that
Host-based routing, header injection and path forwarding all work.

    python3 test/upstream.py            # listens on 127.0.0.1:8080
    python3 test/upstream.py 8081       # listens on 127.0.0.1:8081
"""
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer

DEFAULT_PORT = 8080


class EchoHandler(BaseHTTPRequestHandler):
    def _handle(self):
        body = json.dumps({
            "upstream_port": self.server.server_address[1],
            "host": self.headers.get("Host"),
            "xfh": self.headers.get("X-Forwarded-Host"),
            "xff": self.headers.get("X-Forwarded-For"),
            "path": self.path,
            "method": self.command,
        }).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    do_GET = _handle
    do_POST = _handle
    do_PUT = _handle
    do_DELETE = _handle
    do_HEAD = _handle

    def log_message(self, *args):
        pass


def main():
    port = int(sys.argv[1]) if len(sys.argv) > 1 else DEFAULT_PORT
    HTTPServer(("127.0.0.1", port), EchoHandler).serve_forever()


if __name__ == "__main__":
    main()
