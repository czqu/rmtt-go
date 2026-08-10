package server

import (
	"net"
	"time"

	"golang.org/x/net/websocket"
)

// wsConn adapts a message-based WebSocket connection to a byte-stream net.Conn.
// It buffers one incoming WebSocket message at a time so the RMTT codec can read
// arbitrary-length frames across message boundaries.
type wsConn struct {
	ws  *websocket.Conn
	buf []byte
}

func (c *wsConn) Read(p []byte) (int, error) {
	for len(c.buf) == 0 {
		var data []byte
		if err := websocket.Message.Receive(c.ws, &data); err != nil {
			return 0, err
		}
		c.buf = data
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

func (c *wsConn) Write(p []byte) (int, error) {
	if err := websocket.Message.Send(c.ws, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsConn) Close() error {
	return c.ws.Close()
}

func (c *wsConn) LocalAddr() net.Addr {
	return c.ws.LocalAddr()
}

func (c *wsConn) RemoteAddr() net.Addr {
	return c.ws.RemoteAddr()
}

func (c *wsConn) SetDeadline(t time.Time) error {
	return c.ws.SetDeadline(t)
}

func (c *wsConn) SetReadDeadline(t time.Time) error {
	return c.ws.SetReadDeadline(t)
}

func (c *wsConn) SetWriteDeadline(t time.Time) error {
	return c.ws.SetWriteDeadline(t)
}
