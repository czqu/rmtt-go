package server

import (
	"net"
	"time"

	"github.com/quic-go/quic-go"
)

// quicStreamConn adapts a single QUIC bidirectional stream to a net.Conn.
//
// A QUIC session multiplexes many streams; serveSession accepts each stream
// independently and hands it to the rmtt handler as a separate device
// connection. Therefore Close only closes THIS stream — closing the whole
// session here would tear down every other device sharing the same QUIC
// connection. The session is closed once by serveSession when AcceptStream
// stops (peer gone or listener shutdown).
type quicStreamConn struct {
	*quic.Stream
	session *quic.Conn
}

func (qc *quicStreamConn) LocalAddr() net.Addr {
	return qc.session.LocalAddr()
}

func (qc *quicStreamConn) RemoteAddr() net.Addr {
	return qc.session.RemoteAddr()
}

func (qc *quicStreamConn) SetDeadline(t time.Time) error {
	return qc.Stream.SetDeadline(t)
}

func (qc *quicStreamConn) SetReadDeadline(t time.Time) error {
	return qc.Stream.SetReadDeadline(t)
}

func (qc *quicStreamConn) SetWriteDeadline(t time.Time) error {
	return qc.Stream.SetWriteDeadline(t)
}

func (qc *quicStreamConn) Read(b []byte) (int, error) {
	return qc.Stream.Read(b)
}

func (qc *quicStreamConn) Write(b []byte) (int, error) {
	return qc.Stream.Write(b)
}

func (qc *quicStreamConn) Close() error {
	// Only close this stream. The session is owned by serveSession.
	return qc.Stream.Close()
}
