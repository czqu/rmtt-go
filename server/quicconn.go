package server

import (
	"net"
	"time"

	"github.com/quic-go/quic-go"
)

// quicStreamConn adapts a QUIC bidirectional stream to a net.Conn. Closing the
// connection closes the stream and the underlying QUIC session.
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
	if err := qc.Stream.Close(); err != nil {
		return err
	}
	return qc.session.CloseWithError(0, "")
}
