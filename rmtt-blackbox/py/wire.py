"""Minimal independent RMTT wire-protocol implementation for black-box probes.

Implements the framing (1-byte type|flags header + varint remaining length)
and the six control packets exactly as documented in the protocol:
CONNECT(1) / CONNACK(2) / PUSH(3) / PINGREQ(5) / PINGRESP(6) / DISCONNECT(14).
"""

import socket
import struct
import time

MAGIC = 0x637A7175
CONNECT, CONNACK, PUSH, PINGREQ, PINGRESP, DISCONNECT = 1, 2, 3, 5, 6, 14

CONNACK_TEXT = {
    0: "ACCEPTED",
    1: "BAD_PROTOCOL_VERSION",
    2: "SERVER_UNAVAILABLE",
    3: "NOT_AUTHORISED",
    0xFE: "NETWORK_ERROR",
    0xFF: "PROTOCOL_VIOLATION",
}


class ProtocolError(Exception):
    pass


class Closed(Exception):
    pass


class Timeout(Exception):
    pass


def enc_len(n):
    if n < 0:
        raise ValueError("negative length")
    out = bytearray()
    while True:
        digit = n % 128
        n //= 128
        if n:
            digit |= 0x80
        out.append(digit)
        if not n:
            return bytes(out)


def frame(ptype, body=b"", flags=0):
    return bytes([(ptype << 4) | flags]) + enc_len(len(body)) + body


def connect_body(credential, keepalive=30, version=1, magic=MAGIC, reserved=0):
    cred = credential.encode()
    return (struct.pack("!IBBH", magic, version, reserved, keepalive)
            + struct.pack("!H", len(cred)) + cred)


def connect_packet(credential, keepalive=30, version=1, magic=MAGIC, reserved=0):
    return frame(CONNECT, connect_body(credential, keepalive, version, magic, reserved))


def connack_packet(code, server_keepalive):
    return frame(CONNACK, bytes([code]) + struct.pack("!H", server_keepalive))


def push_packet(payload, reserved=0):
    return frame(PUSH, bytes([reserved]) + payload)


def pingreq_packet():
    return frame(PINGREQ)


def pingresp_packet():
    return frame(PINGRESP)


def disconnect_packet(code):
    return frame(DISCONNECT, bytes([code]))


class Conn:
    """Blocking RMTT client socket with a streaming frame decoder."""

    def __init__(self, host="127.0.0.1", port=19990, timeout=5.0):
        self.sock = socket.create_connection((host, port), timeout=timeout)
        self.sock.settimeout(timeout)
        self.buf = b""
        self.host = host
        self.port = port

    def close(self):
        try:
            self.sock.close()
        except OSError:
            pass

    def send(self, data):
        self.sock.sendall(data)

    def send_slowly(self, data, chunk=1, delay=0.002):
        for i in range(0, len(data), chunk):
            self.sock.sendall(data[i:i + chunk])
            if delay:
                time.sleep(delay)

    def _fill(self, timeout):
        self.sock.settimeout(timeout)
        try:
            chunk = self.sock.recv(65536)
        except socket.timeout:
            raise Timeout("recv timeout")
        if not chunk:
            raise Closed("peer closed")
        self.buf += chunk

    def read_packet(self, timeout=5.0):
        deadline = time.time() + timeout
        # fixed header
        while len(self.buf) < 2:
            self._fill(max(0.001, deadline - time.time()))
        ptype = self.buf[0] >> 4
        flags = self.buf[0] & 0x0F
        idx = 1
        remaining = 0
        mult = 0
        while True:
            if idx >= len(self.buf):
                self._fill(max(0.001, deadline - time.time()))
                continue
            digit = self.buf[idx]
            idx += 1
            remaining |= (digit & 0x7F) << mult
            mult += 7
            if not digit & 0x80:
                break
            if mult >= 28:
                raise ProtocolError("malformed varint")
        while len(self.buf) < idx + remaining:
            self._fill(max(0.001, deadline - time.time()))
        body = self.buf[idx:idx + remaining]
        self.buf = self.buf[idx + remaining:]
        return ptype, flags, body

    def expect(self, ptype, timeout=5.0):
        got, flags, body = self.read_packet(timeout)
        if got != ptype:
            raise ProtocolError("expected packet %d, got %d (flags=%d, body=%r)" % (ptype, got, flags, body))
        return body

    def connect(self, credential, keepalive=30, timeout=5.0, **kw):
        self.send(connect_packet(credential, keepalive, **kw))
        body = self.expect(CONNACK, timeout)
        code = body[0]
        server_kp = struct.unpack("!H", body[1:3])[0]
        return code, server_kp

    def expect_closed(self, timeout=3.0):
        """Return True if the peer closed without sending another packet."""
        try:
            p = self.read_packet(timeout)
        except Closed:
            return True
        except Timeout:
            return False
        raise ProtocolError("expected close, got packet %r" % (p[0],))

    def wait_disconnect(self, timeout=5.0):
        body = self.expect(DISCONNECT, timeout)
        return body[0] if body else None

    def wait_push(self, timeout=5.0):
        body = self.expect(PUSH, timeout)
        return body[0], body[1:]

    def ping(self, timeout=3.0):
        self.send(pingreq_packet())
        body = self.expect(PINGRESP, timeout)
        return body
