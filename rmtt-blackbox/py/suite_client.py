#!/usr/bin/env python3
"""Client-library black-box suite: drives each real client against a scripted
Python wire-protocol server (fake_server.py) and inspects what the client sent.

Usage: suite_client.py --impl go|java-netty|java-aio|rust
Prints TEST lines and a SUMMARY; exits non-zero on failure.
"""

import argparse
import os
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
sys.path.insert(0, HERE)

import wire  # noqa: E402
from fake_server import FakeServer  # noqa: E402

GO_CLIENT = "/tmp/bb-bin/go-client"
RUST_CLIENT = os.path.join(ROOT, "rust-client/target/release/bb-rust-client")
JAVA_NETTY = os.path.join(ROOT, "java-client-netty/target/bb-client-netty.jar")
JAVA_AIO = os.path.join(ROOT, "java-client-aio/target/bb-client-aio.jar")

RESULTS = []
PORT = [28000]


def report(tid, ok, detail=""):
    RESULTS.append((tid, ok, detail))
    print("TEST %s %s %s" % (tid, "PASS" if ok else "FAIL", detail))
    sys.stdout.flush()


def cmd_for(impl, port, cred, scenario, extra):
    if impl == "go":
        return [GO_CLIENT, "-port", str(port), "-credential", cred,
                "-scenario", scenario] + extra
    if impl == "rust":
        return [RUST_CLIENT, "-port", str(port), "-credential", cred,
                "-scenario", scenario] + extra
    jar = JAVA_NETTY if impl == "java-netty" else JAVA_AIO
    return ["java", "-jar", jar, "-port", str(port), "-credential", cred,
            "-scenario", scenario] + extra


def run(impl, cred, scenario, extra, cfg, timeout=40):
    PORT[0] += 1
    port = PORT[0]
    srv = FakeServer(port, **cfg)
    srv.start()
    time.sleep(0.2)
    try:
        p = subprocess.run(cmd_for(impl, port, cred, scenario, extra),
                           capture_output=True, text=True, timeout=timeout)
        out = p.stdout + p.stderr
        time.sleep(0.3)
        return p.returncode, out, srv
    except subprocess.TimeoutExpired as e:
        return 124, (e.stdout or "") + (e.stderr or ""), srv
    finally:
        srv.stop()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--impl", required=True)
    args = ap.parse_args()
    impl = args.impl

    # 1. CONNECT fields + normal echo + clean DISCONNECT
    rc, out, srv = run(impl, "wire-cred", "basic", ["-keepalive", "17"],
                       {"connack_code": 0, "server_kp": 30})
    connects = [r for r in srv.records if "magic" in r]
    disc = [r for r in srv.records if "disconnect" in r]
    ok_fields = (connects and connects[0]["magic"] == wire.MAGIC
                 and connects[0]["version"] == 1
                 and connects[0]["reserved"] == 0
                 and connects[0]["keepalive"] == 17
                 and connects[0]["credential"] == "wire-cred")
    report("client.connect_fields", rc == 0 and ok_fields,
           "rc=%d connect=%r" % (rc, connects[0] if connects else None))
    report("client.clean_disconnect_code", bool(disc) and disc[0]["disconnect"] == 0,
           "disconnect=%r" % (disc[0] if disc else None))

    # 2. CONNACK delivered byte-by-byte
    rc, out, srv = run(impl, "frag-cred", "basic", [],
                       {"connack_code": 0, "server_kp": 30, "split_connack": True})
    report("client.fragmented_connack", rc == 0, "rc=%d" % rc)

    # 3. rejection handling
    rc, out, srv = run(impl, "bad", "authreject", [], {"connack_code": 3, "server_kp": 0})
    report("client.auth_reject", rc == 0 and "CONNECT_REJECTED" in out,
           "rc=%d out=%s" % (rc, out.strip().replace("\n", " | ")[-200:]))

    # 4. server-negotiated keepalive drives PINGREQ cadence
    rc, out, srv = run(impl, "kp-cred", "hold", ["-keepalive", "30", "-wait", "6"],
                       {"connack_code": 0, "server_kp": 2}, timeout=20)
    pings = srv.pings
    gaps = [round(pings[i + 1] - pings[i], 2) for i in range(len(pings) - 1)]
    report("client.keepalive_uses_server_kp", rc == 0 and len(pings) >= 2,
           "rc=%d pings=%d gaps=%r" % (rc, len(pings), gaps))

    # 5. server_kp=0 disables liveness enforcement on the client
    rc, out, srv = run(impl, "nokp-cred", "hold", ["-keepalive", "3", "-wait", "8"],
                       {"connack_code": 0, "server_kp": 0}, timeout=22)
    report("client.server_kp_zero_survives", rc == 0,
           "rc=%d pings=%d" % (rc, len(srv.pings)))

    # 6. downstream 1 MiB PUSH (Java stacks cap frames at 8 KiB)
    rc, out, srv = run(impl, "big-cred", "downlink", ["-wait", "8"],
                       {"connack_code": 0, "server_kp": 30, "downlink": b"x" * (1024 * 1024)},
                       timeout=20)
    report("client.downlink_1MiB", rc == 0 and "EVENT RECV" in out,
           "rc=%d out=%s" % (rc, out.strip().replace("\n", " | ")[-160:]))

    # 7. server-initiated DISCONNECT(0x06) is detected
    rc, out, srv = run(impl, "kick-cred", "kick", ["-wait", "8"],
                       {"connack_code": 0, "server_kp": 30, "disconnect_code": 6,
                        "disconnect_delay": 0.5}, timeout=20)
    report("client.server_disconnect", rc == 0,
           "rc=%d out=%s" % (rc, out.strip().replace("\n", " | ")[-160:]))

    # 8. automatic reconnect after an abrupt transport loss
    rc, out, srv = run(impl, "recon-cred", "reconnect", ["-reconnect", "-keepalive", "2", "-wait", "15"],
                       {"connack_code": 0, "server_kp": 2, "close_after_first_push": True},
                       timeout=35)
    report("client.auto_reconnect", rc == 0 and "RECONNECTED" in out,
           "rc=%d connections=%d out=%s" % (rc, srv.conn_count,
                                            out.strip().replace("\n", " | ")[-200:]))

    failed = [r for r in RESULTS if not r[1]]
    print("SUMMARY client-%s total=%d passed=%d failed=%d"
          % (impl, len(RESULTS), len(RESULTS) - len(failed), len(failed)))
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
