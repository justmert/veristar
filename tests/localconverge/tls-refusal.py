#!/usr/bin/env python3
"""the refusal half of tests/localconverge/tls-audit.sh.

runs inside a throwaway container on one location network of the local
converge, a host on the private network that holds no node certificate.
for each private endpoint it tries:

  plain       the protocol in clear (http, postgresql's startup message,
              a nats client); the server must not serve it
  no cert     tls that trusts the internal ca but presents no client
              certificate; endpoints that ask for one must refuse
  operator    tls with the operators' client certificate, the positive
              control: the endpoint is reachable and answers, so a refusal
              above is the server's decision and not a dead port

    tls-refusal.py <ca.crt> <operator.crt> <operator.key> <targets.json>

prints one json line per attempt and exits 1 when an attempt has the
wrong outcome.
"""

import json
import socket
import ssl
import struct
import sys

CA, CERT, KEY, TARGETS = sys.argv[1:5]
TIMEOUT = 5
failed = False


def ctx(with_cert):
    c = ssl.create_default_context(cafile=CA)
    c.minimum_version = ssl.TLSVersion.TLSv1_3
    if with_cert:
        c.load_cert_chain(CERT, KEY)
    return c


def http_get(sock, host, path):
    sock.sendall(f"GET {path} HTTP/1.1\r\nHost: {host}\r\nConnection: close\r\n\r\n".encode())
    buf = b""
    while len(buf) < 64:
        chunk = sock.recv(4096)
        if not chunk:
            break
        buf += chunk
    return buf


def attempt(t, mode):
    """returns (served, detail): served is true when the endpoint answered
    with its protocol."""
    host, port, kind = t["host"], t["port"], t["kind"]
    raw = socket.create_connection((host, port), timeout=TIMEOUT)
    try:
        if mode == "plain":
            if kind == "postgres":
                # startup message, protocol 3.0, user postgres, no sslrequest
                body = struct.pack("!I", 196608) + b"user\0postgres\0database\0postgres\0\0"
                raw.sendall(struct.pack("!I", len(body) + 4) + body)
                r = raw.recv(4096)
                # 'R' is an authentication request: the server went on in clear
                return r[:1] == b"R", r[:120].decode(errors="replace")
            if kind == "nats":
                # a plain client waits for INFO; a tls-first server sends none
                try:
                    r = raw.recv(4096)
                except socket.timeout:
                    return False, "no INFO in clear within %ds" % TIMEOUT
                return r.startswith(b"INFO"), r[:60].decode(errors="replace")
            if kind == "raw":
                raw.sendall(b"PING\r\n")
                try:
                    r = raw.recv(4096)
                except socket.timeout:
                    return False, "no answer in clear"
                return len(r) > 0 and not r[:1] == b"\x15", repr(r[:40])
            r = http_get(raw, host, t.get("path", "/"))
            return r.startswith(b"HTTP/") and b" 400 " not in r[:16], r[:60].decode(errors="replace")
        s = ctx(mode == "operator").wrap_socket(raw, server_hostname=host)
        if kind in ("nats", "raw", "postgres"):
            # these speak first or wait for a protocol line; a completed
            # handshake that is not torn down within a read is served
            s.settimeout(2)
            try:
                r = s.recv(4096)
            except socket.timeout:
                return True, "handshake completed"
            return len(r) > 0, repr(r[:40])
        r = http_get(s, host, t.get("path", "/"))
        return r.startswith(b"HTTP/"), r[:40].decode(errors="replace")
    except (ssl.SSLError, ConnectionResetError, BrokenPipeError, socket.timeout, OSError) as e:
        return False, type(e).__name__ + ": " + str(e)[:120]
    finally:
        raw.close()


with open(TARGETS) as f:
    targets = json.load(f)
for t in targets:
    for mode in t["modes"]:
        want = mode == "operator" or (mode == "no cert" and not t.get("client_cert_required", True))
        served, detail = attempt(t, mode)
        ok = served == want
        failed |= not ok
        print(json.dumps({"endpoint": t["name"], "address": "%s:%d" % (t["host"], t["port"]), "mode": mode,
                          "served": served, "expected_served": want, "ok": ok, "detail": detail}))
sys.exit(1 if failed else 0)
