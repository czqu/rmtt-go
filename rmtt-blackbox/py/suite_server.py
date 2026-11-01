#!/usr/bin/env python3
"""Protocol-level black-box suite: drives any rmtt server with an independent
wire implementation (no server library code involved).

Usage:
  suite_server.py --host 127.0.0.1 --port N --control C --name go \
      [--kp-min 30 --kp-max 600 --kp-default 60] [--allow-disable] \
      [--reject-cred bad] [--phase core|keepalive|shutdown|all]

Prints one `TEST <id> PASS|FAIL <detail>` line per case and exits non-zero if
any case failed.
"""

import argparse
import base64
import os
import socket
import sys
import time

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from wire import (  # noqa: E402
    CONNACK, CONNECT, DISCONNECT, PINGRESP, PUSH, Closed, Conn, ProtocolError, Timeout,
    disconnect_packet, pingreq_packet, push_packet,
)

RESULTS = []


def report(tid, ok, detail=""):
    RESULTS.append((tid, ok, detail))
    print("TEST %s %s %s" % (tid, "PASS" if ok else "FAIL", detail))
    sys.stdout.flush()


class Control:
    def __init__(self, port):
        self.sock = socket.create_connection(("127.0.0.1", port), timeout=5)
        self.f = self.sock.makefile("rwb", buffering=0)

    def cmd(self, line, timeout=5):
        self.sock.settimeout(timeout)
        self.f.write((line + "\n").encode())
        return self.f.readline().decode(errors="replace").strip()

    def close(self):
        try:
            self.f.close()
            self.sock.close()
        except OSError:
            pass


def kp_expect(client_kp, mn, mx, dflt, allow_disable):
    if client_kp <= 0:
        return 0 if allow_disable else dflt
    return max(mn, min(mx, client_kp))


def expect_closed_or_disconnect(c, code, timeout=3.0):
    """Accept DISCONNECT(code) followed by close, or a bare close."""
    try:
        body = c.expect(DISCONNECT, timeout)
        got = body[0] if body else None
        return got == code, "disconnect=0x%02x" % (got if got is not None else 255)
    except (Closed, ConnectionError, OSError) as e:
        return True, "closed (%s)" % type(e).__name__
    except Timeout:
        return False, "no DISCONNECT/close within %.1fs (socket still open)" % timeout
    except ProtocolError as e:
        return False, str(e)


def core_suite(args):
    host, port = args.host, args.port
    mn, mx, dflt = args.kp_min, args.kp_max, args.kp_default
    control = Control(args.control) if args.control else None

    def connect(cred, kp=30, timeout=5, **kw):
        c = Conn(host, port, timeout)
        code, skp = c.connect(cred, kp, timeout, **kw)
        return c, code, skp

    # 1. valid handshake + keepalive clamp low
    try:
        c, code, skp = connect("dev-basic", 10)
        want = kp_expect(10, mn, mx, dflt, args.allow_disable)
        report("handshake.accepted", code == 0 and skp == want,
               "code=%d server_kp=%d want_kp=%d" % (code, skp, want))
        c.send(disconnect_packet(0))
        c.close()
    except Exception as e:
        report("handshake.accepted", False, repr(e))

    # 2. keepalive clamp high
    try:
        c, code, skp = connect("dev-high", 10000)
        want = kp_expect(10000, mn, mx, dflt, args.allow_disable)
        report("handshake.kp_high", code == 0 and skp == want,
               "code=%d server_kp=%d want_kp=%d" % (code, skp, want))
        c.close()
    except Exception as e:
        report("handshake.kp_high", False, repr(e))

    # 3. keepalive zero -> default (or disabled)
    try:
        c, code, skp = connect("dev-zero", 0)
        want = kp_expect(0, mn, mx, dflt, args.allow_disable)
        report("handshake.kp_zero", code == 0 and skp == want,
               "code=%d server_kp=%d want_kp=%d" % (code, skp, want))
        c.close()
    except Exception as e:
        report("handshake.kp_zero", False, repr(e))

    # 4. keepalive mid-range passes through
    mid = max(mn + 1, min(mx - 1, (mn + mx) // 2)) if mx > mn + 1 else mn
    try:
        c, code, skp = connect("dev-mid", mid)
        want = kp_expect(mid, mn, mx, dflt, args.allow_disable)
        report("handshake.kp_passthrough", code == 0 and skp == want,
               "proposal=%d server_kp=%d want_kp=%d" % (mid, skp, want))
        c.close()
    except Exception as e:
        report("handshake.kp_passthrough", False, repr(e))

    # 5. bad magic: silent close, no CONNACK
    try:
        c = Conn(host, port)
        c.send(__import__("wire").connect_packet("dev-magic", 30, magic=0xDEADBEEF))
        try:
            p = c.read_packet(3)
            report("handshake.bad_magic", False, "unexpected packet type=%d" % p[0])
        except Closed:
            report("handshake.bad_magic", True, "closed without CONNACK")
        c.close()
    except Exception as e:
        report("handshake.bad_magic", False, repr(e))

    # 6. bad protocol version -> CONNACK 0x01 then close
    try:
        c = Conn(host, port)
        c.send(__import__("wire").connect_packet("dev-ver", 30, version=2))
        code, skp = None, None
        try:
            body = c.expect(CONNACK, 3)
            code, skp = body[0], int.from_bytes(body[1:3], "big")
        except (Closed, Timeout):
            pass
        report("handshake.bad_version", code == 1, "connack=%r" % (code,))
        c.close()
    except Exception as e:
        report("handshake.bad_version", False, repr(e))

    # 7. rejected credential (only when the server is configured to reject)
    if args.reject_cred:
        try:
            c, code, _ = connect(args.reject_cred, 30)
            report("handshake.auth_reject", code == 3, "connack=0x%02x" % code)
            c.close()
        except Exception as e:
            report("handshake.auth_reject", False, repr(e))

    # 7b. credential length boundary (Java caps at 23 bytes, Go/Rust at 65535)
    try:
        c, code, _ = connect("c" * 23, 30)
        report("handshake.credential_23", code == 0, "connack=0x%02x" % code)
        c.close()
    except Exception as e:
        report("handshake.credential_23", False, repr(e))
    try:
        c = Conn(host, port)
        code, _ = c.connect("c" * 24, 30)
        want = 3 if args.cred_limit == 23 else 0
        report("handshake.credential_24", code == want,
               "connack=0x%02x want=0x%02x" % (code, want))
        c.close()
    except Exception as e:
        report("handshake.credential_24", False, repr(e))

    # 8. PINGREQ -> PINGRESP
    try:
        c, code, _ = connect("dev-ping", 30)
        body = c.ping(3)
        report("control.ping", True, "pingresp len=%d" % len(body))
        c.close()
    except Exception as e:
        report("control.ping", False, repr(e))

    # 9. uplink PUSH echo (text)
    try:
        c, code, _ = connect("dev-echo", 30)
        c.send(push_packet(b"hello-wire"))
        reserved, payload = c.wait_push(5)
        report("push.echo_text", payload == b"echo:hello-wire", "payload=%r" % payload)
        c.close()
    except Exception as e:
        report("push.echo_text", False, repr(e))

    # 10. empty payload
    try:
        c, code, _ = connect("dev-empty", 30)
        c.send(push_packet(b""))
        reserved, payload = c.wait_push(5)
        report("push.echo_empty", payload == b"echo:", "payload=%r" % payload)
        c.close()
    except Exception as e:
        report("push.echo_empty", False, repr(e))

    # 11. binary payload (all byte values)
    try:
        c, code, _ = connect("dev-bin", 30)
        payload = bytes(range(256))
        c.send(push_packet(payload))
        reserved, got = c.wait_push(5)
        report("push.echo_binary", got == b"echo:" + payload, "len=%d" % len(got))
        c.close()
    except Exception as e:
        report("push.echo_binary", False, repr(e))

    # 12. 8000-byte payload (below the Java 8 KiB default, above typical buffers)
    try:
        c, code, _ = connect("dev-8k", 30)
        payload = bytes((i * 7) & 0xFF for i in range(8000))
        c.send(push_packet(payload))
        reserved, got = c.wait_push(8)
        report("push.echo_8000", got == b"echo:" + payload, "len=%d" % len(got))
        c.close()
    except Exception as e:
        report("push.echo_8000", False, repr(e))

    # 13. CONNECT delivered one byte at a time (streaming decoder)
    try:
        import wire
        c = Conn(host, port)
        c.send_slowly(wire.connect_packet("dev-frag", 30), chunk=1, delay=0.003)
        body = c.expect(CONNACK, 8)
        report("stream.fragmented_connect", body[0] == 0, "connack=0x%02x" % body[0])
        c.close()
    except Exception as e:
        report("stream.fragmented_connect", False, repr(e))

    # 14. two PUSH frames coalesced in one TCP segment
    try:
        c, code, _ = connect("dev-coalesce", 30)
        c.send(push_packet(b"co-1") + push_packet(b"co-2"))
        _, p1 = c.wait_push(5)
        _, p2 = c.wait_push(5)
        report("stream.coalesced_push",
               {p1, p2} == {b"echo:co-1", b"echo:co-2"},
               "%r %r" % (p1, p2))
        report("stream.order_preserved", p1 == b"echo:co-1" and p2 == b"echo:co-2",
               "pipelined order %r then %r" % (p1, p2))
        c.close()
    except Exception as e:
        report("stream.coalesced_push", False, repr(e))
        report("stream.order_preserved", False, repr(e))

    # 15. session takeover: old connection gets DISCONNECT(0x02)
    try:
        c1, code1, _ = connect("dev-takeover", 30)
        c2, code2, _ = connect("dev-takeover", 30)
        ok, detail = expect_closed_or_disconnect(c1, 0x02, 4)
        body = c2.ping(3)
        report("session.takeover", ok and body == b"", "old=%s new_ping=%r" % (detail, body))
        c1.close()
        c2.close()
    except Exception as e:
        report("session.takeover", False, repr(e))

    # 16. control-socket downstream push
    if control:
        try:
            c, code, _ = connect("dev-down", 30)
            r = control.cmd("PUSH dev-down server-push")
            _, payload = c.wait_push(5)
            report("server.push_downlink", r == "OK" and payload == b"server-push",
                   "control=%r payload=%r" % (r, payload))
            c.close()
        except Exception as e:
            report("server.push_downlink", False, repr(e))

        # 17. control-socket kick with reason 0x06
        try:
            c, code, _ = connect("dev-kick", 30)
            time.sleep(0.2)
            r = control.cmd("KICK dev-kick 6")
            ok, detail = expect_closed_or_disconnect(c, 0x06, 4)
            report("server.kick", r == "OK" and ok, "control=%r %s" % (r, detail))
            c.close()
        except Exception as e:
            report("server.kick", False, repr(e))

        # 18. push to an offline device is rejected
        try:
            r = control.cmd("PUSH ghost-device nope")
            report("server.push_offline", r.startswith("ERR"), "control=%r" % r)
        except Exception as e:
            report("server.push_offline", False, repr(e))

        # 19. online state tracking
        try:
            c, code, _ = connect("dev-online", 30)
            time.sleep(0.2)
            r1 = control.cmd("ONLINE dev-online")
            c.send(disconnect_packet(0))
            c.close()
            time.sleep(0.4)
            r2 = control.cmd("ONLINE dev-online")
            report("server.online_state", "true" in r1 and "false" in r2,
                   "before=%r after=%r" % (r1, r2))
        except Exception as e:
            report("server.online_state", False, repr(e))

    # 20. protocol violation mid-stream -> DISCONNECT(0x04)
    try:
        c, code, _ = connect("dev-violation", 30)
        c.send(b"\x40\x00")  # unknown packet type 4, zero flags
        ok, detail = expect_closed_or_disconnect(c, 0x04, 4)
        report("protocol.unknown_type_disconnect", ok, detail)
        c.close()
    except Exception as e:
        report("protocol.unknown_type_disconnect", False, repr(e))

    # 20b. non-zero fixed-header flags (0xFF) -> clean close or DISCONNECT(0x04)
    try:
        c, code, _ = connect("dev-flags", 30)
        c.send(b"\xff\x00")
        ok, detail = expect_closed_or_disconnect(c, 0x04, 4)
        report("protocol.nonzero_flags", ok, detail)
        c.close()
    except Exception as e:
        report("protocol.nonzero_flags", False, repr(e))

    # 21. unknown packet type -> close (optionally DISCONNECT 0x04)
    try:
        c, code, _ = connect("dev-unknown", 30)
        c.send(b"\x40\x00")  # type 4, length 0
        ok, detail = expect_closed_or_disconnect(c, 0x04, 4)
        report("protocol.unknown_type", ok, detail)
        c.close()
    except Exception as e:
        report("protocol.unknown_type", False, repr(e))

    # 22. oversized length claim (16 MiB + 1) -> close / disconnect
    try:
        c, code, _ = connect("dev-oversize", 30)
        # type PUSH + varint 16MiB+1 (0x81000001 -> 0x81 0x80 0x80 0x08)
        c.send(b"\x30\x81\x80\x80\x08")
        ok, detail = expect_closed_or_disconnect(c, 0x04, 5)
        report("protocol.oversized_length", ok, detail)
        c.close()
    except Exception as e:
        report("protocol.oversized_length", False, repr(e))

    # 23. multi-device isolation
    try:
        a, _, _ = connect("dev-a", 30)
        b, _, _ = connect("dev-b", 30)
        a.send(push_packet(b"from-a"))
        b.send(push_packet(b"from-b"))
        _, pa = a.wait_push(5)
        _, pb = b.wait_push(5)
        report("session.multi_device_isolation",
               pa == b"echo:from-a" and pb == b"echo:from-b", "%r %r" % (pa, pb))
        a.close()
        b.close()
    except Exception as e:
        report("session.multi_device_isolation", False, repr(e))

    # 24. clean disconnect is reported offline (control)
    if control:
        try:
            c, code, _ = connect("dev-clean", 30)
            time.sleep(0.2)
            before = control.cmd("ONLINE dev-clean")
            c.send(disconnect_packet(0))
            c.close()
            time.sleep(0.4)
            after = control.cmd("ONLINE dev-clean")
            report("session.clean_disconnect", "true" in before and "false" in after,
                   "%r -> %r" % (before, after))
        except Exception as e:
            report("session.clean_disconnect", False, repr(e))

    # 25. 20 concurrent devices, each with its own echo
    try:
        conns = []
        for i in range(20):
            c, code, _ = connect("dev-many-%d" % i, 30)
            conns.append(c)
        for i, c in enumerate(conns):
            c.send(push_packet(("many-%d" % i).encode()))
        ok = True
        for i, c in enumerate(conns):
            _, p = c.wait_push(8)
            if p != ("echo:many-%d" % i).encode():
                ok = False
        report("session.20_concurrent_devices", ok, "20 devices")
        for c in conns:
            c.close()
    except Exception as e:
        report("session.20_concurrent_devices", False, repr(e))

    if control:
        control.close()


def keepalive_suite(args):
    """Requires a server configured with keepalive min=max=default=1s."""
    try:
        c = Conn(args.host, args.port)
        code, skp = c.connect("dev-silent", 1)
        t0 = time.time()
        try:
            body = c.expect(DISCONNECT, 5)
            elapsed = time.time() - t0
            code_dc = body[0] if body else None
            report("keepalive.silent_reaped", code == 0 and code_dc == 0x05 and 1.0 <= elapsed <= 4.0,
                   "connack=0x%02x disconnect=0x%02x after=%.2fs" % (code, code_dc or 0, elapsed))
        except (Closed, Timeout) as e:
            report("keepalive.silent_reaped", False, "no DISCONNECT(0x05): %r" % e)
        c.close()
    except Exception as e:
        report("keepalive.silent_reaped", False, repr(e))

    # A client that pings must survive past 1.5x the keepalive.
    try:
        c = Conn(args.host, args.port)
        code, skp = c.connect("dev-pinger", 1)
        ok = True
        deadline = time.time() + 4.0
        while time.time() < deadline:
            time.sleep(0.8)
            try:
                c.ping(2)
            except Exception as e:
                ok = False
                report("keepalive.ping_keeps_alive", False, "ping failed after %.1fs: %r" % (4.0 - (deadline - time.time()), e))
                break
        if ok:
            report("keepalive.ping_keeps_alive", True, "survived 4s with server_kp=%d" % skp)
        c.close()
    except Exception as e:
        report("keepalive.ping_keeps_alive", False, repr(e))


def kpzero_suite(args):
    """Keepalive=0 with AllowDisable: CONNACK must carry server_kp=0 and the
    connection must survive without any heartbeat."""
    try:
        c = Conn(args.host, args.port)
        code, skp = c.connect("dev-kpzero", 0)
        report("keepalive.disable_negotiated", code == 0 and skp == 0,
               "connack=0x%02x server_kp=%d" % (code, skp))
        c.send(push_packet(b"zero-1"))
        _, p1 = c.wait_push(3)
        time.sleep(2.5)
        c.send(push_packet(b"zero-2"))
        _, p2 = c.wait_push(3)
        report("keepalive.disable_not_reaped", p1 == b"echo:zero-1" and p2 == b"echo:zero-2",
               "%r %r" % (p1, p2))
        c.close()
    except Exception as e:
        report("keepalive.disable_not_reaped", False, repr(e))


def shutdown_suite(args):
    """Requires control. Server is expected to send DISCONNECT(0x03) to clients."""
    try:
        control = Control(args.control)
        c = Conn(args.host, args.port)
        code, skp = c.connect("dev-shutdown", 30)
        time.sleep(0.2)
        r = control.cmd("SHUTDOWN")
        try:
            body = c.expect(DISCONNECT, 5)
            dcode = body[0] if body else None
            report("server.shutdown_code", r == "OK" and dcode == 0x03,
                   "control=%r disconnect=0x%02x" % (r, dcode if dcode is not None else 255))
        except (Closed, Timeout) as e:
            report("server.shutdown_code", False, "no DISCONNECT(0x03): %r" % e)
        c.close()
        control.close()
    except Exception as e:
        report("server.shutdown_code", False, repr(e))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--port", type=int, required=True)
    ap.add_argument("--control", type=int, default=0)
    ap.add_argument("--name", default="server")
    ap.add_argument("--kp-min", type=int, default=30)
    ap.add_argument("--kp-max", type=int, default=600)
    ap.add_argument("--kp-default", type=int, default=60)
    ap.add_argument("--allow-disable", action="store_true")
    ap.add_argument("--reject-cred", default="")
    ap.add_argument("--cred-limit", type=int, default=0, help="23 for Java servers")
    ap.add_argument("--phase", default="all")
    args = ap.parse_args()

    if args.phase in ("core", "all"):
        core_suite(args)
    if args.phase in ("keepalive", "all"):
        keepalive_suite(args)
    if args.phase in ("kpzero", "all") and args.allow_disable:
        kpzero_suite(args)
    if args.phase in ("shutdown", "all") and args.control:
        shutdown_suite(args)

    failed = [r for r in RESULTS if not r[1]]
    print("SUMMARY %s total=%d passed=%d failed=%d"
          % (args.name, len(RESULTS), len(RESULTS) - len(failed), len(failed)))
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
