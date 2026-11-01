"""A scriptable, independent RMTT server used to test client libraries.

It speaks only the wire protocol (no rmtt library) and records exactly what a
client sends: CONNECT fields, PUSH payloads, PINGREQ timestamps and the final
DISCONNECT reason code.
"""

import socket
import struct
import threading
import time

import wire


class FakeServer(threading.Thread):
    def __init__(self, port, connack_code=0, server_kp=30, split_connack=False,
                 echo=True, downlink=None, downlink_delay=0.3,
                 close_after_first_push=False, disconnect_code=None,
                 disconnect_delay=0.5):
        super().__init__(daemon=True)
        self.port = port
        self.connack_code = connack_code
        self.server_kp = server_kp
        self.split_connack = split_connack
        self.echo = echo
        self.downlink = downlink
        self.downlink_delay = downlink_delay
        self.close_after_first_push = close_after_first_push
        self.disconnect_code = disconnect_code
        self.disconnect_delay = disconnect_delay

        self.records = []
        self.pings = []
        self.connects = []
        self.closed = False
        self.sock = None
        self.conn_count = 0
        self._stop = threading.Event()

    # ---- lifecycle -------------------------------------------------
    def run(self):
        self.sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.sock.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        self.sock.bind(("127.0.0.1", self.port))
        self.sock.listen(16)
        self.sock.settimeout(0.2)
        while not self._stop.is_set():
            try:
                c, _ = self.sock.accept()
            except socket.timeout:
                continue
            except OSError:
                return
            self.conn_count += 1
            idx = self.conn_count
            threading.Thread(target=self.handle, args=(c, idx), daemon=True).start()

    def stop(self):
        self._stop.set()
        try:
            if self.sock:
                self.sock.close()
        except OSError:
            pass

    # ---- connection handling --------------------------------------
    def read_packet(self, c, buf, timeout=10.0):
        c.settimeout(timeout)
        deadline = time.time() + timeout
        while True:
            if len(buf) >= 2:
                ptype = buf[0] >> 4
                flags = buf[0] & 0x0F
                idx = 1
                remaining = 0
                mult = 0
                ok = False
                while idx < len(buf) and mult < 28:
                    d = buf[idx]
                    idx += 1
                    remaining |= (d & 0x7F) << mult
                    mult += 7
                    if not d & 0x80:
                        ok = True
                        break
                if ok and len(buf) >= idx + remaining:
                    body = buf[idx:idx + remaining]
                    return ptype, flags, body, buf[idx + remaining:]
            if time.time() > deadline:
                raise TimeoutError("fake server read timeout")
            chunk = c.recv(65536)
            if not chunk:
                raise ConnectionError("client closed")
            buf += chunk

    def handle(self, c, idx):
        buf = b""
        try:
            ptype, flags, body, buf = self.read_packet(c, buf)
            if ptype != wire.CONNECT:
                self.records.append({"conn": idx, "error": "first packet %d" % ptype})
                return
            magic, version, reserved, kp = struct.unpack("!IBBH", body[:8])
            clen = struct.unpack("!H", body[8:10])[0]
            credential = body[10:10 + clen].decode(errors="replace")
            info = {
                "conn": idx, "magic": magic, "version": version, "reserved": reserved,
                "keepalive": kp, "credential": credential,
                "connect_at": time.time(),
            }
            self.connects.append(info)
            self.records.append(info)

            packet = wire.connack_packet(self.connack_code, self.server_kp)
            if self.split_connack:
                for b in packet:
                    c.sendall(bytes([b]))
                    time.sleep(0.02)
            else:
                c.sendall(packet)
            if self.connack_code != 0:
                time.sleep(0.1)
                return

            if self.downlink is not None:
                threading.Thread(target=self.send_downlink, args=(c,), daemon=True).start()
            if self.disconnect_code is not None:
                threading.Thread(target=self.send_disconnect, args=(c,), daemon=True).start()

            pushed = 0
            while True:
                ptype, flags, body, buf = self.read_packet(c, buf, timeout=30)
                if ptype == wire.PINGREQ:
                    self.pings.append(time.time())
                    c.sendall(wire.pingresp_packet())
                elif ptype == wire.PUSH:
                    payload = body[1:]
                    self.records.append({"conn": idx, "push": payload})
                    if self.echo:
                        c.sendall(wire.push_packet(b"echo:" + payload))
                        pushed += 1
                        if self.close_after_first_push and pushed == 1:
                            time.sleep(0.4)
                            try:
                                c.shutdown(socket.SHUT_RDWR)
                            except OSError:
                                pass
                            return
                elif ptype == wire.DISCONNECT:
                    self.records.append({"conn": idx, "disconnect": body[0] if body else None})
                    return
                elif ptype == wire.PINGRESP:
                    pass
                else:
                    self.records.append({"conn": idx, "unexpected_type": ptype})
                    return
        except (ConnectionError, TimeoutError, OSError):
            pass
        finally:
            try:
                c.close()
            except OSError:
                pass

    def send_downlink(self, c):
        time.sleep(self.downlink_delay)
        try:
            c.sendall(wire.push_packet(self.downlink))
            self.records.append({"downlink_sent": len(self.downlink)})
        except OSError:
            pass

    def send_disconnect(self, c):
        time.sleep(self.disconnect_delay)
        try:
            c.sendall(wire.disconnect_packet(self.disconnect_code))
            self.records.append({"disconnect_sent": self.disconnect_code})
        except OSError:
            pass
