#!/usr/bin/env python3
# test_ws_stream.py — /ws/cams endpoint smoke test.
#
# Connects to ws://<ip>/ws/cams as a single viewer and validates
# the streaming protocol end-to-end:
#
#   1. RFC 6455 handshake completes (101 Switching Protocols,
#      Sec-WebSocket-Accept verified against SHA-1(key+GUID)).
#   2. The first frame is a TEXT hello with the REQ-WS-002
#      schema (type=hello, mac, name, fw, caps).
#   3. The next N frames are BINARY JPEG payloads with valid
#      SOI (FF D8) / EOI (FF D9) markers and reasonable size.
#   4. The frames are NOT frozen — SHA-1 hashes distinct.
#
# Exits 0 on success, 1 on any failure. Pure stdlib so the
# script runs anywhere Python 3.7+ runs (no websocket-client /
# websockets / wsproto dependency).
#
# Usage:
#   python3 scripts/test_ws_stream.py 192.168.1.199
#   python3 scripts/test_ws_stream.py 192.168.1.199 --frames 30
#   python3 scripts/test_ws_stream.py 192.168.1.199 --timeout 30
#
# Author: el-gentleman. Lands on feat/iot-cams-ws-cams-endpoint
# alongside the /ws/cams endpoint so future operators have a
# one-shot validation tool without pulling websocat.
from __future__ import annotations

import argparse
import base64
import hashlib
import json
import os
import socket
import struct
import sys
import time
from typing import Optional, Tuple

# RFC 6455 magic GUID, concatenated with the client's
# Sec-WebSocket-Key for the Sec-WebSocket-Accept hash.
WS_GUID = b"258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

# Default path (matches CONFIG_FIRMWARE_WS_PATH in
# provisioning/Kconfig.projbuild).
DEFAULT_PATH = "/ws/cams"

# JPEG markers.
JPEG_SOI = b"\xff\xd8"
JPEG_EOI = b"\xff\xd9"

# A reasonable JPEG should be at least 1 KB; the AI-Thinker
# SVGA q12 frames measured around 28 KB (engram 4336).
MIN_JPEG_BYTES = 1024


def make_ws_key() -> Tuple[bytes, str]:
    """Generate a 16-byte random key and its base64 form. The
    raw bytes are kept so the caller can base64-decode the
    server's Sec-WebSocket-Accept value to spot-check against
    the underlying SHA-1 input, but the canonical verifier
    uses the base64 string per RFC 6455 §1.3 (the server
    concatenates the GUID with the value of the
    Sec-WebSocket-Key header, which IS the base64 form)."""
    raw = os.urandom(16)
    return raw, base64.b64encode(raw).decode("ascii")


def build_handshake(host: str, path: str, key_b64: str) -> bytes:
    """HTTP/1.1 upgrade request for a WebSocket client."""
    return (
        f"GET {path} HTTP/1.1\r\n"
        f"Host: {host}\r\n"
        f"Upgrade: websocket\r\n"
        f"Connection: Upgrade\r\n"
        f"Sec-WebSocket-Key: {key_b64}\r\n"
        f"Sec-WebSocket-Version: 13\r\n"
        f"User-Agent: iot_cams-test-ws-stream/1.0\r\n"
        f"\r\n"
    ).encode("ascii")


def read_http_response(sock: socket.socket,
                        timeout: float) -> Tuple[int, dict, bytes]:
    """Read the HTTP response status line + headers + the bytes
    already-buffered past \\r\\n\\r\\n. Returns (status, headers, leftover).
    Body content is not consumed; WS upgrade has no body."""
    sock.settimeout(timeout)
    buf = bytearray()
    while b"\r\n\r\n" not in buf:
        chunk = sock.recv(4096)
        if not chunk:
            raise ConnectionError("server closed during handshake")
        buf.extend(chunk)
        if len(buf) > 8192:
            raise ValueError("handshake response too large (>8 KB)")

    head, _, leftover = bytes(buf).partition(b"\r\n\r\n")
    lines = head.split(b"\r\n")
    if not lines:
        raise ValueError("empty handshake response")
    try:
        version, code_str, _ = lines[0].split(b" ", 2)
        status = int(code_str)
    except ValueError as e:
        raise ValueError(f"malformed status line: {lines[0]!r}") from e
    if not version.startswith(b"HTTP/1."):
        raise ValueError(f"unexpected HTTP version: {version!r}")

    headers: dict = {}
    for raw in lines[1:]:
        if b":" not in raw:
            continue
        k, _, v = raw.partition(b":")
        headers[k.strip().lower().decode("ascii")] = v.strip().decode("ascii")
    return status, headers, leftover


def verify_accept(headers: dict, key_b64: str) -> None:
    """Verify the Sec-WebSocket-Accept header.

    RFC 6455 §1.3 (Opening Handshake):
    > The server takes the value of the Sec-WebSocket-Key header
    > field, appends the GUID "258EAFA5-E914-47DA-95CA-C5AB0DC85B11",
    > SHA-1 hashes, base64-encodes, returns as Sec-WebSocket-Accept.

    Note that the value of Sec-WebSocket-Key IS the base64-encoded
    nonce; the server does NOT decode it back to the 16 raw bytes
    before concatenating with the GUID. Verified empirically against
    the ESP-IDF v5.5 /ws/cams endpoint at 192.168.1.199 (matches
    when computed against the base64 string; mismatches when
    computed against the raw 16 bytes — bug fix 2026-09-28).
    """
    expected = base64.b64encode(
        hashlib.sha1(key_b64.encode("ascii") + WS_GUID).digest()
    ).decode("ascii")
    actual = headers.get("sec-websocket-accept")
    if actual != expected:
        raise ValueError(
            f"Sec-WebSocket-Accept mismatch: got {actual!r}, "
            f"expected {expected!r}"
        )


def read_frame(sock: socket.socket,
               initial: bytes,
               timeout: float,
               max_payload: int = 1 << 20) -> Tuple[int, bytes, bytes]:
    """Read one WS frame. Returns (opcode, payload, leftover_bytes).

    Server-to-client frames MUST NOT be masked per RFC 6455 §5.1;
    we reject any masked frame as a protocol error.

    Loop on partial frames (FIN=0) until a complete message is
    assembled — though the /ws/cams endpoint only emits finished
    frames (`pkt.final=true`), so the loop is a one-shot in
    practice. Implemented for protocol correctness.
    """
    sock.settimeout(timeout)
    buf = bytearray(initial)
    final_op: Optional[int] = None
    payload = bytearray()

    while True:
        # Ensure we have at least 2 bytes for the base header.
        while len(buf) < 2:
            chunk = sock.recv(4096)
            if not chunk:
                raise ConnectionError("server closed mid-frame")
            buf.extend(chunk)

        b0, b1 = buf[0], buf[1]
        fin = (b0 >> 7) & 0x01
        rsv = (b0 >> 4) & 0x07
        if rsv:
            raise ValueError("RSV bits set; server should not extend protocol")
        opcode = b0 & 0x0F
        masked = (b1 >> 7) & 0x01
        plen = b1 & 0x7F

        # Validate the first fragment of a message: opcode must
        # be a known data opcode; continuation frames (opcode 0)
        # are only valid after the first fragment.
        if final_op is None:
            if opcode not in (0x0, 0x1, 0x2, 0x8, 0x9, 0xA):
                raise ValueError(f"unknown opcode: {opcode:#x}")
        else:
            if opcode != 0x0:
                raise ValueError(
                    f"opcode {opcode:#x} in continuation; expected 0x0"
                )

        if masked:
            raise ValueError("server-to-client frame must not be masked")
        if plen == 126:
            while len(buf) < 4:
                chunk = sock.recv(4096)
                if not chunk:
                    raise ConnectionError("server closed mid-ext-len")
                buf.extend(chunk)
            plen = struct.unpack(">H", bytes(buf[2:4]))[0]
            consumed = 4
        elif plen == 127:
            while len(buf) < 10:
                chunk = sock.recv(4096)
                if not chunk:
                    raise ConnectionError("server closed mid-ext-len")
                buf.extend(chunk)
            plen = struct.unpack(">Q", bytes(buf[2:10]))[0]
            consumed = 10
        else:
            consumed = 2

        if plen > max_payload:
            raise ValueError(
                f"payload length {plen} > max {max_payload}"
            )

        # Read the payload.
        while len(buf) < consumed + plen:
            chunk = sock.recv(4096)
            if not chunk:
                raise ConnectionError("server closed mid-payload")
            buf.extend(chunk)

        payload.extend(bytes(buf[consumed:consumed + plen]))
        del buf[:consumed + plen]

        if final_op is None:
            final_op = opcode

        if fin:
            return final_op, bytes(payload), bytes(buf)


def is_valid_jpeg(data: bytes) -> Tuple[bool, str]:
    """Quick SOI/EOI sanity check — NOT a full JPEG validator.
    A corrupt payload that nevertheless carries the right
    markers will pass; a corrupt payload missing them will not.
    The check is sufficient to catch dropped/replaced/corrupt
    buffers on the wire."""
    if len(data) < MIN_JPEG_BYTES:
        return False, f"too short ({len(data)} B < {MIN_JPEG_BYTES} B)"
    if not data.startswith(JPEG_SOI):
        return False, "missing SOI marker (FF D8)"
    if not data.endswith(JPEG_EOI):
        return False, "missing EOI marker (FF D9)"
    return True, "ok"


def run_smoke(ip: str, port: int, path: str,
              frames_expected: int, timeout: float) -> int:
    print(f"[test] connecting ws://{ip}:{port}{path}")
    t0 = time.monotonic()
    sock = socket.create_connection((ip, port), timeout=timeout)

    key_raw, key_b64 = make_ws_key()
    sock.sendall(build_handshake(f"{ip}:{port}", path, key_b64))

    status, headers, leftover = read_http_response(sock, timeout)
    if status != 101:
        sock.close()
        print(f"[fail] expected HTTP 101 Switching Protocols, got {status}")
        if "body" in headers:
            print(f"       headers: {headers}")
        return 1
    verify_accept(headers, key_b64)
    del key_raw  # not used past this point; suppress linter noise
    print(f"[ok]   handshake: HTTP {status} in "
          f"{(time.monotonic() - t0) * 1000:.1f} ms")

    # Frame 1 — hello (TEXT).
    try:
        opcode, payload, leftover = read_frame(sock, leftover, timeout)
    except (ConnectionError, ValueError) as e:
        sock.close()
        print(f"[fail] hello frame: {e}")
        return 1

    if opcode != 0x1:
        sock.close()
        print(f"[fail] first frame opcode {opcode:#x} (expected TEXT 0x1)")
        return 1
    try:
        hello = json.loads(payload.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError) as e:
        sock.close()
        print(f"[fail] hello JSON parse: {e}")
        return 1

    for required in ("type", "mac", "name", "fw", "caps"):
        if required not in hello:
            sock.close()
            print(f"[fail] hello missing required field {required!r}")
            return 1
    if hello["type"] != "hello":
        sock.close()
        print(f"[fail] hello.type is {hello['type']!r} (expected 'hello')")
        return 1
    print(f"[ok]   hello: mac={hello['mac']} name={hello['name']!r} "
          f"fw={hello['fw']} caps={hello['caps']}")

    # Frames 2..N+1 — binary JPEGs.
    digests: dict = {}
    received = 0
    fails = 0
    bytes_total = 0
    t_first = None
    t_last = None

    while received < frames_expected:
        try:
            opcode, payload, leftover = read_frame(sock, leftover, timeout)
        except (ConnectionError, ValueError) as e:
            print(f"[fail] binary frame {received + 1}: {e}")
            fails += 1
            break
        if opcode != 0x2:
            print(f"[fail] frame {received + 1} opcode {opcode:#x} "
                  "(expected BINARY 0x2)")
            fails += 1
            continue

        ok, why = is_valid_jpeg(payload)
        if not ok:
            print(f"[fail] frame {received + 1} not a valid JPEG: {why}")
            fails += 1
            continue

        sha = hashlib.sha1(payload).hexdigest()
        digests[sha] = digests.get(sha, 0) + 1
        received += 1
        bytes_total += len(payload)
        now = time.monotonic()
        if t_first is None:
            t_first = now
        t_last = now

    sock.close()
    elapsed = (t_last - t_first) if (t_first and t_last) else 0.0
    fps = received / elapsed if elapsed > 0 else 0.0
    bitrate_kbps = (bytes_total * 8 / 1024) / elapsed if elapsed > 0 else 0.0
    distinct = len(digests)
    dupes = sum(c - 1 for c in digests.values() if c > 1)

    print()
    print(f"[summary] received:    {received} / {frames_expected} frames")
    print(f"[summary] jpeg ok:     {received - fails} / {received}")
    print(f"[summary] distinct:    {distinct} unique SHA-1s "
          f"({dupes} duplicates)")
    print(f"[summary] bytes:       {bytes_total} "
          f"({bytes_total // max(received, 1)} B/frame avg)")
    if elapsed > 0:
        print(f"[summary] fps:         {fps:.2f} "
              f"(over {elapsed:.2f} s)")
        print(f"[summary] bitrate:     {bitrate_kbps:.1f} kbps")
    print(f"[summary] total time:  {time.monotonic() - t0:.2f} s")

    if fails or received < frames_expected or distinct < received:
        print()
        print(f"[RESULT] FAIL "
              f"(received={received}, expected={frames_expected}, "
              f"fails={fails}, distinct={distinct})")
        return 1

    print()
    print(f"[RESULT] PASS")
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(
        description="iot_cams /ws/cams smoke test",
    )
    parser.add_argument("ip", nargs="?",
                        default="192.168.1.199",
                        help="device IP (default: 192.168.1.199)")
    parser.add_argument("--port", type=int, default=80,
                        help="device httpd port (default: 80)")
    parser.add_argument("--path", default=DEFAULT_PATH,
                        help=f"WS path (default: {DEFAULT_PATH})")
    parser.add_argument("--frames", type=int, default=10,
                        help="binary frames to validate (default: 10)")
    parser.add_argument("--timeout", type=float, default=5.0,
                        help="per-read timeout in seconds (default: 5.0)")
    args = parser.parse_args()

    try:
        return run_smoke(
            ip=args.ip,
            port=args.port,
            path=args.path,
            frames_expected=args.frames,
            timeout=args.timeout,
        )
    except (ConnectionRefusedError, socket.timeout, OSError) as e:
        print(f"[fail] socket: {e}")
        return 1
    except KeyboardInterrupt:
        print("\n[abort] interrupted")
        return 130


if __name__ == "__main__":
    sys.exit(main())
