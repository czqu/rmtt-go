// Package transport adapts message- and stream-oriented transports to the
// byte-stream net.Conn interface that the rmtt codec reads and writes. The
// adapters are shared by the client and server so the framing semantics
// cannot drift between them.
package transport

import (
	"net"
	"time"

	"golang.org/x/net/websocket"
)

// WSConn adapts a message-based WebSocket connection to a byte-stream
// net.Conn. It buffers one incoming WebSocket message at a time so the rmtt
// codec can read arbitrary-length frames across message boundaries.
type WSConn struct {
	ws  *websocket.Conn
	buf []byte
}

// NewWSConn wraps a WebSocket connection as a byte-stream net.Conn.
func NewWSConn(ws *websocket.Conn) *WSConn {
	return &WSConn{ws: ws}
}

func (c *WSConn) Read(p []byte) (int, error) {
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

func (c *WSConn) Write(p []byte) (int, error) {
	if err := websocket.Message.Send(c.ws, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *WSConn) Close() error {
	return c.ws.Close()
}

func (c *WSConn) LocalAddr() net.Addr {
	return c.ws.LocalAddr()
}

func (c *WSConn) RemoteAddr() net.Addr {
	return c.ws.RemoteAddr()
}

func (c *WSConn) SetDeadline(t time.Time) error {
	return c.ws.SetDeadline(t)
}

func (c *WSConn) SetReadDeadline(t time.Time) error {
	return c.ws.SetReadDeadline(t)
}

func (c *WSConn) SetWriteDeadline(t time.Time) error {
	return c.ws.SetWriteDeadline(t)
}
