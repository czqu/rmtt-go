package transport

import (
	"net"
	"time"

	"github.com/quic-go/quic-go"
)

// QUICStreamConn adapts a single QUIC bidirectional stream to a net.Conn.
//
// A QUIC session multiplexes many streams, so Close closes only this stream.
// Callers that own the whole session pass a closeHook (e.g. closing the
// session to release the UDP socket); callers that share the session with
// other device streams pass nil.
type QUICStreamConn struct {
	*quic.Stream
	session   *quic.Conn
	closeHook func() error
}

// NewQUICStreamConn wraps a QUIC stream as a net.Conn. Close closes the
// stream and then runs closeHook, if any.
func NewQUICStreamConn(stream *quic.Stream, session *quic.Conn, closeHook func() error) *QUICStreamConn {
	return &QUICStreamConn{Stream: stream, session: session, closeHook: closeHook}
}

func (qc *QUICStreamConn) LocalAddr() net.Addr {
	return qc.session.LocalAddr()
}

func (qc *QUICStreamConn) RemoteAddr() net.Addr {
	return qc.session.RemoteAddr()
}

func (qc *QUICStreamConn) SetDeadline(t time.Time) error {
	return qc.Stream.SetDeadline(t)
}

func (qc *QUICStreamConn) SetReadDeadline(t time.Time) error {
	return qc.Stream.SetReadDeadline(t)
}

func (qc *QUICStreamConn) SetWriteDeadline(t time.Time) error {
	return qc.Stream.SetWriteDeadline(t)
}

func (qc *QUICStreamConn) Read(b []byte) (int, error) {
	return qc.Stream.Read(b)
}

func (qc *QUICStreamConn) Write(b []byte) (int, error) {
	return qc.Stream.Write(b)
}

func (qc *QUICStreamConn) Close() error {
	err := qc.Stream.Close()
	if qc.closeHook != nil {
		if hookErr := qc.closeHook(); err == nil {
			err = hookErr
		}
	}
	return err
}
