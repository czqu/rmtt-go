#!/usr/bin/env python3
"""Cross-language black-box E2E orchestrator for the rmtt language matrix.

Starts real servers (Go / Java-Netty / Java-AIO / Rust) and runs real clients
(Go / Java-Netty / Java-AIO / Rust) against them, plus an independent Python
wire-level protocol suite for every server.

Results are written to results/results.json and results/results.txt.
"""

import argparse
import json
import os
import shutil
import socket
import subprocess
import sys
import time

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
LOGDIR = "/tmp/bb-logs"
RESDIR = os.path.join(ROOT, "results")

GO_SERVER = "/tmp/bb-bin/go-server"
GO_CLIENT = "/tmp/bb-bin/go-client"
JAVA_SERVER_NETTY = os.path.join(ROOT, "java-server-netty/target/bb-server-netty.jar")
JAVA_SERVER_AIO = os.path.join(ROOT, "java-server-aio/target/bb-server-aio.jar")
JAVA_CLIENT_NETTY = os.path.join(ROOT, "java-client-netty/target/bb-client-netty.jar")
JAVA_CLIENT_AIO = os.path.join(ROOT, "java-client-aio/target/bb-client-aio.jar")
RUST_SERVER = os.path.join(ROOT, "rust-server/target/release/bb-rust-server")
RUST_CLIENT = os.path.join(ROOT, "rust-client/target/release/bb-rust-client")
CERT = os.path.join(ROOT, "certs/cert.pem")
KEY = os.path.join(ROOT, "certs/key.pem")

SERVERS = ["go", "java-netty", "java-aio", "rust"]
CLIENTS = ["go", "java-netty", "java-aio", "rust"]

SERVER_TRANSPORTS = {
    "go": {"tcp", "tls", "ws", "wss", "kcp", "quic"},
    "java-netty": {"tcp", "ws", "wss", "kcp"},
    "java-aio": {"tcp", "tls", "ws", "wss"},
    "rust": {"tcp"},
}
CLIENT_TRANSPORTS = {
    "go": {"tcp", "tls", "ws", "wss", "kcp", "quic"},
    "java-netty": {"tcp", "ws", "wss", "kcp"},
    "java-aio": {"tcp", "tls", "ws", "wss"},
    "rust": {"tcp", "tls"},
}

RESULTS = []
PORT = [30000]


def next_ports():
    PORT[0] += 1
    return PORT[0], PORT[0] + 3000


def add(suite, server, client, transport, case, ok, detail, dur, evidence=""):
    RESULTS.append({
        "suite": suite,
        "server": server,
        "client": client,
        "transport": transport,
        "case": case,
        "status": "PASS" if ok else "FAIL",
        "detail": detail,
        "duration_s": round(dur, 2),
        "evidence": evidence[-3000:],
    })
    print("[%s] %-11s %-12s %-14s %-22s %s"
          % ("PASS" if ok else "FAIL", suite, server, client + "/" + transport, case, detail))
    sys.stdout.flush()


def tail(path, n=4000):
    try:
        with open(path, "r", errors="replace") as f:
            return f.read()[-n:]
    except OSError:
        return ""


def wait_file(path, marker, timeout):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if marker in tail(path, 200000):
            return True
        time.sleep(0.1)
    return False


class Server:
    def __init__(self, impl, transport, port, control, extra=None, tag=""):
        self.impl = impl
        self.transport = transport
        self.port = port
        self.control = control
        self.extra = extra or []
        self.log = os.path.join(LOGDIR, "srv-%s-%s-%d%s.log" % (impl, transport, port, tag))
        self.proc = None

    def cmd(self):
        if self.impl == "go":
            return [GO_SERVER, "-transport", self.transport, "-port", str(self.port),
                    "-control", str(self.control)] + self.extra
        if self.impl == "rust":
            return [RUST_SERVER, "-port", str(self.port), "-control", str(self.control)] + self.extra
        jar = JAVA_SERVER_NETTY if self.impl == "java-netty" else JAVA_SERVER_AIO
        return ["java", "-jar", jar, "-transport", self.transport, "-port", str(self.port),
                "-control", str(self.control)] + self.extra

    def start(self, timeout=30):
        if os.path.exists(self.log):
            os.remove(self.log)
        self.proc = subprocess.Popen(self.cmd(), stdout=open(self.log, "w"),
                                     stderr=subprocess.STDOUT, start_new_session=True)
        if not wait_file(self.log, "READY", timeout):
            raise RuntimeError("server %s/%s did not become ready: %s" %
                               (self.impl, self.transport, tail(self.log, 1500)))
        return self

    def stop(self):
        if self.proc is None:
            return
        try:
            self.proc.terminate()
            self.proc.wait(timeout=2)
        except Exception:
            try:
                self.proc.kill()
                self.proc.wait(timeout=3)
            except Exception:
                pass
        self.proc = None

    def control_cmd(self, line, timeout=5):
        s = socket.create_connection(("127.0.0.1", self.control), timeout=timeout)
        s.settimeout(timeout)
        f = s.makefile("rwb", buffering=0)
        f.write((line + "\n").encode())
        resp = f.readline().decode(errors="replace").strip()
        try:
            f.close()
            s.close()
        except OSError:
            pass
        return resp


def client_cmd(impl, transport, port, cred, scenario, extra=None):
    extra = extra or []
    if impl == "go":
        return [GO_CLIENT, "-transport", transport, "-port", str(port),
                "-credential", cred, "-scenario", scenario] + extra
    if impl == "rust":
        return [RUST_CLIENT, "-transport", transport, "-port", str(port),
                "-credential", cred, "-scenario", scenario] + extra
    jar = JAVA_CLIENT_NETTY if impl == "java-netty" else JAVA_CLIENT_AIO
    return ["java", "-jar", jar, "-transport", transport, "-port", str(port),
            "-credential", cred, "-scenario", scenario] + extra


def spawn_client(impl, transport, port, cred, scenario, extra, tag):
    log = os.path.join(LOGDIR, "cli-%s-%s-%d-%s.log" % (impl, transport, port, tag))
    if os.path.exists(log):
        os.remove(log)
    p = subprocess.Popen(client_cmd(impl, transport, port, cred, scenario, extra),
                         stdout=open(log, "w"), stderr=subprocess.STDOUT, start_new_session=True)
    return p, log


def run_client(impl, transport, port, cred, scenario, extra=None, timeout=45):
    p, log = spawn_client(impl, transport, port, cred, scenario, extra, "sync")
    try:
        p.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        p.kill()
        p.wait()
        return 124, tail(log), log
    return p.returncode, tail(log), log


def case_client(suite, s, c, transport, case, cred, scenario, extra=None,
                server_extra=None, timeout=45, kp_server=False):
    port, control = next_ports()
    if kp_server:
        se = ["-keepalive-min", "5", "-keepalive-max", "5", "-keepalive-default", "5"]
        if server_extra:
            se += server_extra
    else:
        se = ["-reject", "badcred"] + (server_extra or [])
    srv = Server(s, transport, port, control, se)
    t0 = time.time()
    try:
        srv.start()
        rc, out, log = run_client(c, transport, port, cred, scenario, extra, timeout)
        ok = rc == 0 and "RESULT PASS" in out
        add(suite, s, c, transport, case, ok, "rc=%d" % rc, time.time() - t0, out)
    except Exception as e:
        add(suite, s, c, transport, case, False, repr(e), time.time() - t0, tail(srv.log))
    finally:
        srv.stop()


def case_downlink(s, c, transport="tcp"):
    port, control = next_ports()
    srv = Server(s, transport, port, control, ["-reject", "badcred"])
    t0 = time.time()
    cred = "p%d-d" % port
    p = None
    try:
        srv.start()
        p, log = spawn_client(c, transport, port, cred, "downlink", ["-wait", "10"], "down")
        if not wait_file(log, "EVENT CONNECTED", 15):
            raise RuntimeError("client did not connect: " + tail(log, 800))
        resp = srv.control_cmd("PUSH %s server-push" % cred)
        p.wait(timeout=15)
        out = tail(log)
        ok = p.returncode == 0 and resp == "OK" and "RESULT PASS" in out
        add("matrix", s, c, transport, "downlink", ok, "control=%r" % resp,
            time.time() - t0, out)
    except Exception as e:
        add("matrix", s, c, transport, "downlink", False, repr(e), time.time() - t0, tail(srv.log))
    finally:
        try:
            p.kill()
        except Exception:
            pass
        srv.stop()


def case_kick(s, c, transport="tcp"):
    port, control = next_ports()
    srv = Server(s, transport, port, control, ["-reject", "badcred"])
    t0 = time.time()
    cred = "p%d-k" % port
    p = None
    try:
        srv.start()
        p, log = spawn_client(c, transport, port, cred, "kick", ["-wait", "10"], "kick")
        if not wait_file(log, "EVENT READY", 15):
            raise RuntimeError("kick client not ready: " + tail(log, 800))
        resp = srv.control_cmd("KICK %s 6" % cred)
        p.wait(timeout=15)
        out = tail(log)
        ok = p.returncode == 0 and resp == "OK" and "RESULT PASS" in out
        add("matrix", s, c, transport, "kick", ok, "control=%r" % resp, time.time() - t0, out)
    except Exception as e:
        add("matrix", s, c, transport, "kick", False, repr(e), time.time() - t0,
            tail(srv.log) + (tail(log) if p else ""))
    finally:
        if p:
            try:
                p.kill()
            except Exception:
                pass
        srv.stop()


def case_takeover(s, c, transport="tcp"):
    port, control = next_ports()
    srv = Server(s, transport, port, control, ["-reject", "badcred"])
    t0 = time.time()
    cred = "p%d-t" % port
    victim = None
    try:
        srv.start()
        victim, vlog = spawn_client(c, transport, port, cred, "kick", ["-wait", "12"], "victim")
        if not wait_file(vlog, "EVENT READY", 15):
            raise RuntimeError("victim not ready: " + tail(vlog, 800))
        rc2, aout, alog = run_client(c, transport, port, cred, "takeover-attacker",
                                     ["-wait", "3"], timeout=25)
        victim.wait(timeout=20)
        vout = tail(vlog)
        ok = (victim.returncode == 0 and "RESULT PASS" in vout
              and rc2 == 0 and "RESULT PASS" in aout)
        add("matrix", s, c, transport, "session_takeover", ok,
            "victim_rc=%d attacker_rc=%d" % (victim.returncode, rc2),
            time.time() - t0, "VICTIM:\n" + vout + "\nATTACKER:\n" + aout)
    except Exception as e:
        add("matrix", s, c, transport, "session_takeover", False, repr(e),
            time.time() - t0, tail(srv.log))
    finally:
        if victim:
            try:
                victim.kill()
            except Exception:
                pass
        srv.stop()


def case_reconnect(s, c, transport="tcp"):
    port, control = next_ports()
    recon_cfg = ["-keepalive-min", "2", "-keepalive-max", "2", "-keepalive-default", "2"]
    srv = Server(s, transport, port, control, ["-reject", "badcred"] + recon_cfg, tag="-r1")
    t0 = time.time()
    cred = "p%d-r" % port
    p = None
    try:
        srv.start()
        p, log = spawn_client(c, transport, port, cred, "reconnect",
                              ["-reconnect", "-keepalive", "2", "-wait", "20"], "recon")
        if not wait_file(log, "EVENT READY_FOR_KILL", 20):
            raise RuntimeError("client never reached kill point: " + tail(log, 1200))
        srv.stop()
        time.sleep(0.4)
        srv2 = Server(s, transport, port, control, ["-reject", "badcred"] + recon_cfg, tag="-r2")
        srv2.start()
        srv = srv2
        p.wait(timeout=45)
        out = tail(log)
        ok = p.returncode == 0 and "RESULT PASS" in out and "RECONNECTED" in out
        add("matrix", s, c, transport, "auto_reconnect", ok, "rc=%d" % p.returncode,
            time.time() - t0, out)
    except Exception as e:
        add("matrix", s, c, transport, "auto_reconnect", False, repr(e), time.time() - t0,
            (tail(log) if p else "") + "\n" + tail(srv.log))
    finally:
        if p:
            try:
                p.kill()
            except Exception:
                pass
        srv.stop()


def run_combo(s, c):
    print("\n===== %s server <-> %s client =====" % (s, c))
    transport = "tcp"
    port, control = next_ports()
    srv = Server(s, transport, port, control, ["-reject", "badcred"])
    t0 = time.time()
    try:
        srv.start()
        for case, scenario, extra in [
            ("connect_echo", "basic", []),
            ("binary_256", "binary", []),
            ("medium_4000", "large", ["-size", "4000"]),
            ("concurrent_5", "concurrent", ["-count", "5"]),
            ("large_1MiB", "large", ["-size", "1048576"]),
        ]:
            cred = "p%d%s" % (port, case[0])
            tc = time.time()
            rc, out, log = run_client(c, transport, port, cred, scenario, extra, timeout=45)
            add("matrix", s, c, transport, case, rc == 0 and "RESULT PASS" in out,
                "rc=%d" % rc, time.time() - tc, out)
        # auth reject
        tc = time.time()
        rc, out, log = run_client(c, transport, port, "badcred", "authreject", [], timeout=20)
        add("matrix", s, c, transport, "auth_reject", rc == 0 and "RESULT PASS" in out,
            "rc=%d" % rc, time.time() - tc, out)
    except Exception as e:
        add("matrix", s, c, transport, "server_session", False, repr(e),
            time.time() - t0, tail(srv.log))
    finally:
        srv.stop()

    case_downlink(s, c, transport)
    case_kick(s, c, transport)
    case_takeover(s, c, transport)
    case_client("matrix", s, c, transport, "keepalive_5s", "kp-dev",
                "keepalive", ["-keepalive", "5"], kp_server=True, timeout=35)
    case_reconnect(s, c, transport)


def run_transport_pair(s, c, transport):
    print("\n===== transport %s: %s <-> %s =====" % (transport, s, c))
    port, control = next_ports()

    def extra_for(impl):
        if transport in ("tls", "wss", "quic"):
            return ["-cert", CERT, "-key", KEY]
        return []

    srv = Server(s, transport, port, control, ["-reject", "badcred"] + extra_for(s))
    t0 = time.time()
    try:
        srv.start()
        for case, scenario, extra in [
            ("connect_echo", "basic", []),
            ("medium_4000", "large", ["-size", "4000"]),
        ]:
            cred = "p%d%s" % (port, case[0])
            tc = time.time()
            rc, out, log = run_client(c, transport, port, cred, scenario, extra, timeout=45)
            add("transport", s, c, transport, case, rc == 0 and "RESULT PASS" in out,
                "rc=%d" % rc, time.time() - tc, out)
    except Exception as e:
        add("transport", s, c, transport, "server_session", False, repr(e),
            time.time() - t0, tail(srv.log))
    finally:
        srv.stop()
    case_downlink_transport(s, c, transport)


def case_downlink_transport(s, c, transport):
    port, control = next_ports()
    cert_extra = ["-cert", CERT, "-key", KEY] if transport in ("tls", "wss", "quic") else []
    srv = Server(s, transport, port, control, ["-reject", "badcred"] + cert_extra)
    t0 = time.time()
    cred = "p%d-d" % port
    p = None
    try:
        srv.start()
        p, log = spawn_client(c, transport, port, cred, "downlink", ["-wait", "10"], "down")
        if not wait_file(log, "EVENT CONNECTED", 15):
            raise RuntimeError("no connect: " + tail(log, 800))
        resp = srv.control_cmd("PUSH %s server-push" % cred)
        p.wait(timeout=15)
        out = tail(log)
        add("transport", s, c, transport, "downlink",
            p.returncode == 0 and resp == "OK" and "RESULT PASS" in out,
            "control=%r" % resp, time.time() - t0, out)
    except Exception as e:
        add("transport", s, c, transport, "downlink", False, repr(e), time.time() - t0,
            tail(srv.log) + (tail(log) if p else ""))
    finally:
        if p:
            try:
                p.kill()
            except Exception:
                pass
        srv.stop()


def run_protocol(impl):
    print("\n===== protocol suite: %s =====" % impl)
    port, control = next_ports()
    srv = Server(impl, "tcp", port, control, ["-reject", "badcred"])
    t0 = time.time()
    cred_limit = "23" if impl.startswith("java") else "0"
    try:
        srv.start()
        out = subprocess.run([sys.executable, os.path.join(ROOT, "py", "suite_server.py"),
                              "--port", str(port), "--control", str(control), "--name", impl,
                              "--reject-cred", "badcred", "--cred-limit", cred_limit,
                              "--phase", "core"],
                             capture_output=True, text=True, timeout=180).stdout
        for line in out.splitlines():
            if line.startswith("TEST "):
                parts = line.split(" ", 3)
                add("protocol", impl, "-", "tcp", parts[1], parts[2] == "PASS",
                    parts[3] if len(parts) > 3 else "", time.time() - t0, line)
    except Exception as e:
        add("protocol", impl, "-", "tcp", "core_suite", False, repr(e), time.time() - t0, tail(srv.log))
    finally:
        srv.stop()

    # keepalive enforcement with a 1s policy
    port, control = next_ports()
    srv = Server(impl, "tcp", port, control,
                 ["-keepalive-min", "1", "-keepalive-max", "2", "-keepalive-default", "1"])
    t0 = time.time()
    try:
        srv.start()
        out = subprocess.run([sys.executable, os.path.join(ROOT, "py", "suite_server.py"),
                              "--port", str(port), "--name", impl, "--phase", "keepalive",
                              "--kp-min", "1", "--kp-max", "2", "--kp-default", "1"],
                             capture_output=True, text=True, timeout=120).stdout
        for line in out.splitlines():
            if line.startswith("TEST "):
                parts = line.split(" ", 3)
                add("protocol", impl, "-", "tcp", parts[1], parts[2] == "PASS",
                    parts[3] if len(parts) > 3 else "", time.time() - t0, line)
    except Exception as e:
        add("protocol", impl, "-", "tcp", "keepalive_suite", False, repr(e), time.time() - t0,
            tail(srv.log))
    finally:
        srv.stop()

    # shutdown code (server process exits)
    port, control = next_ports()
    srv = Server(impl, "tcp", port, control)
    t0 = time.time()
    try:
        srv.start()
        out = subprocess.run([sys.executable, os.path.join(ROOT, "py", "suite_server.py"),
                              "--port", str(port), "--control", str(control), "--name", impl,
                              "--phase", "shutdown"],
                             capture_output=True, text=True, timeout=60).stdout
        for line in out.splitlines():
            if line.startswith("TEST "):
                parts = line.split(" ", 3)
                add("protocol", impl, "-", "tcp", parts[1], parts[2] == "PASS",
                    parts[3] if len(parts) > 3 else "", time.time() - t0, line)
    except Exception as e:
        add("protocol", impl, "-", "tcp", "shutdown_suite", False, repr(e), time.time() - t0,
            tail(srv.log))
    finally:
        srv.stop()

    # keepalive disable semantics
    port, control = next_ports()
    srv = Server(impl, "tcp", port, control,
                 ["-allow-disable", "-keepalive-min", "1", "-keepalive-max", "2",
                  "-keepalive-default", "1"])
    t0 = time.time()
    try:
        srv.start()
        out = subprocess.run([sys.executable, os.path.join(ROOT, "py", "suite_server.py"),
                              "--port", str(port), "--name", impl, "--phase", "kpzero",
                              "--allow-disable", "--kp-min", "1", "--kp-max", "2",
                              "--kp-default", "1"],
                             capture_output=True, text=True, timeout=60).stdout
        for line in out.splitlines():
            if line.startswith("TEST "):
                parts = line.split(" ", 3)
                add("protocol", impl, "-", "tcp", parts[1], parts[2] == "PASS",
                    parts[3] if len(parts) > 3 else "", time.time() - t0, line)
    except Exception as e:
        add("protocol", impl, "-", "tcp", "kpzero_suite", False, repr(e), time.time() - t0,
            tail(srv.log))
    finally:
        srv.stop()


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--only", default="all", help="all|matrix|transport|protocol")
    ap.add_argument("--servers", default=",".join(SERVERS))
    ap.add_argument("--clients", default=",".join(CLIENTS))
    args = ap.parse_args()

    os.makedirs(LOGDIR, exist_ok=True)
    os.makedirs(RESDIR, exist_ok=True)
    servers = [s for s in args.servers.split(",") if s]
    clients = [c for c in args.clients.split(",") if c]
    t_start = time.time()

    if args.only in ("all", "protocol"):
        for s in servers:
            run_protocol(s)

    if args.only in ("all", "matrix"):
        for s in servers:
            for c in clients:
                run_combo(s, c)

    if args.only in ("all", "transport"):
        for s in servers:
            for c in clients:
                common = SERVER_TRANSPORTS[s] & CLIENT_TRANSPORTS[c]
                for transport in ["tls", "ws", "wss", "kcp", "quic"]:
                    if transport in common:
                        run_transport_pair(s, c, transport)

    passed = sum(1 for r in RESULTS if r["status"] == "PASS")
    total = len(RESULTS)
    print("\n================ SUMMARY ================")
    print("total=%d passed=%d failed=%d elapsed=%.1fs" % (total, passed, total - passed,
                                                          time.time() - t_start))
    with open(os.path.join(RESDIR, "results.json"), "w") as f:
        json.dump({"total": total, "passed": passed, "failed": total - passed,
                   "elapsed_s": round(time.time() - t_start, 1), "results": RESULTS}, f, indent=2)
    with open(os.path.join(RESDIR, "results.txt"), "w") as f:
        for r in RESULTS:
            f.write("%-6s %-10s %-12s %-14s %-24s %s\n" % (
                r["status"], r["suite"], r["server"], r["client"] + "/" + r["transport"],
                r["case"], r["detail"]))
    sys.exit(0 if passed == total else 1)


if __name__ == "__main__":
    main()
