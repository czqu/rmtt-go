package client

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/czqu/rmtt-go/codec"
	"github.com/quic-go/quic-go"
	"github.com/xtaci/kcp-go"
	"golang.org/x/net/websocket"
)

const closedNetConnErrorText = "use of closed network connection"

type quicConn struct {
	*quic.Stream
	session *quic.Conn
}

// quicConf returns the quic-go transport configuration. When override is nil
// the library's hardened defaults are used; otherwise the caller's Config is
// used (SetQuicConfig already guarantees KeepAlivePeriod > 0). Application
// heartbeats can legitimately stretch far past quic-go's
// 30s default idle timeout; without a transport-level keepalive an otherwise
// healthy connection would be torn down during that quiet gap. KeepAlivePeriod
// emits a lightweight packet periodically so the idle timer never fires.
//
// WARNING: if you supply your own *quic.Config you take full responsibility for
// its values — any Config with KeepAlivePeriod <= 0 or MaxIdleTimeout shorter
// than the application's heartbeat/report interval reproduces the periodic
// "timeout: no recent network activity" drops. The library refuses the former
// in SetQuicConfig; the latter is on you.
func quicConf(override *quic.Config) *quic.Config {
	if override != nil {
		return override
	}
	return &quic.Config{
		MaxIdleTimeout:  15 * time.Minute,
		KeepAlivePeriod: 30 * time.Second,
	}
}

func (qc *quicConn) LocalAddr() net.Addr {
	return qc.session.LocalAddr()
}

func (qc *quicConn) RemoteAddr() net.Addr {
	return qc.session.RemoteAddr()
}

func (qc *quicConn) SetDeadline(t time.Time) error {
	return qc.Stream.SetDeadline(t)
}

func (qc *quicConn) SetReadDeadline(t time.Time) error {
	return qc.Stream.SetReadDeadline(t)
}

func (qc *quicConn) SetWriteDeadline(t time.Time) error {
	return qc.Stream.SetWriteDeadline(t)
}

func (qc *quicConn) Read(b []byte) (int, error) {
	return qc.Stream.Read(b)
}

func (qc *quicConn) Write(b []byte) (int, error) {
	return qc.Stream.Write(b)
}

func (qc *quicConn) Close() error {
	err := qc.Stream.Close()
	if err != nil {
		return err
	}
	return qc.session.CloseWithError(0, "")
}

func openConnection(uri *url.URL, tlsc *tls.Config, dialer *net.Dialer, quicConfig *quic.Config) (net.Conn, error) {
	switch uri.Scheme {
	case "tcp":
		conn, err := dialer.Dial("tcp", uri.Host)
		if err != nil {
			return nil, err
		}
		return conn, nil
	case "kcp":
		kcpConn, err := kcp.Dial(uri.Host)
		if err != nil {
			return nil, err
		}
		return kcpConn, nil
	case "tls":
		conn, err := dialer.Dial("tcp", uri.Host)
		if err != nil {
			return nil, err
		}
		tlsConn := tls.Client(conn, tlsc)
		err = tlsConn.Handshake()
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return tlsConn, nil
	case "quic":
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		qc := quicConf(quicConfig)
		session, err := quic.DialAddr(ctx, uri.Host, tlsc, qc)
		if err != nil {
			return nil, err
		}
		stream, err := session.OpenStreamSync(context.Background())
		if err != nil {
			return nil, err
		}
		return &quicConn{Stream: stream, session: session}, nil
	case "ws":
		return dialWebSocket(uri.Host, uri.Path, "ws", nil)
	case "wss":
		return dialWebSocket(uri.Host, uri.Path, "wss", tlsc)
	}
	return nil, errors.New("unknown protocol")
}

type wsStreamConn struct {
	ws  *websocket.Conn
	buf []byte
}

func dialWebSocket(host, path, scheme string, tlsConfig *tls.Config) (net.Conn, error) {
	if path == "" {
		path = "/rmtt"
	}
	loc, err := url.Parse(scheme + "://" + host + path)
	if err != nil {
		return nil, err
	}
	cfg := &websocket.Config{
		Location:  loc,
		Origin:    &url.URL{Host: host, Scheme: "http"},
		Protocol:  []string{"rmtt"},
		Version:   websocket.ProtocolVersionHybi13,
		TlsConfig: tlsConfig,
	}
	ws, err := websocket.DialConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &wsStreamConn{ws: ws}, nil
}

func (c *wsStreamConn) Read(p []byte) (int, error) {
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

func (c *wsStreamConn) Write(p []byte) (int, error) {
	if err := websocket.Message.Send(c.ws, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsStreamConn) Close() error {
	return c.ws.Close()
}

func (c *wsStreamConn) LocalAddr() net.Addr {
	return c.ws.LocalAddr()
}

func (c *wsStreamConn) RemoteAddr() net.Addr {
	return c.ws.RemoteAddr()
}

func (c *wsStreamConn) SetDeadline(t time.Time) error {
	return c.ws.SetDeadline(t)
}

func (c *wsStreamConn) SetReadDeadline(t time.Time) error {
	return c.ws.SetReadDeadline(t)
}

func (c *wsStreamConn) SetWriteDeadline(t time.Time) error {
	return c.ws.SetWriteDeadline(t)
}

func connectServer(conn io.ReadWriter, cm *codec.ConnectPacket, protocolVersion uint) (byte, uint16, error) {
	DEBUG.Println(NET, "connect started")
	if err := cm.Write(conn); err != nil {
		ERROR.Println(err)
		return codec.ErrNetworkError, 0, err
	}
	rc, serverKp, err := verifyCONNACK(conn)
	return rc, serverKp, err
}

func verifyCONNACK(conn io.Reader) (byte, uint16, error) {
	DEBUG.Println(NET, "waiting for CONNACK")
	ca, err := codec.ReadPacket(conn)
	if err != nil {
		ERROR.Println(NET, "connect got error", err)
		return codec.ErrNetworkError, 0, err
	}
	if ca == nil {
		ERROR.Println(NET, "received nil packet")
		return codec.ErrNetworkError, 0, errors.New("nil CONNACK packet")
	}
	msg, ok := ca.(*codec.ConnackPacket)
	if !ok {
		ERROR.Println(NET, "received msg that was not CONNACK")
		return codec.ErrNetworkError, 0, errors.New("non-CONNACK first packet received")
	}
	DEBUG.Println(NET, "received connack")
	return msg.ReturnCode, msg.ServerKeepalive, nil
}

func ackFunc(oboundP chan *PacketAndToken, packet *codec.PushPacket) func() {
	return func() {
		// do nothing, since there is no need to send an ack packet back
	}
}

type commsFns interface {
	UpdateLastReceived()
	UpdateLastSent()
	getWriteTimeOut() time.Duration
	CloseConnect(reason byte)
}

func startComms(conn net.Conn,
	c commsFns,
	inboundFromStore <-chan codec.ControlPacket,
	oboundp <-chan *PacketAndToken,
	obound <-chan *PacketAndToken,
) (<-chan *codec.PushPacket, <-chan error) {
	ibound := startIncomingComms(conn, c, inboundFromStore)
	outboundFromIncoming := make(chan *PacketAndToken)

	oboundErr := startOutgoingComms(conn, c, oboundp, obound, outboundFromIncoming)
	DEBUG.Println(NET, "startComms started")

	var wg sync.WaitGroup
	wg.Add(2)

	outPublish := make(chan *codec.PushPacket)
	outError := make(chan error)

	go func() {
		for ic := range ibound {
			if ic.err != nil {
				outError <- ic.err
				continue
			}
			if ic.outbound != nil {
				outboundFromIncoming <- ic.outbound
				continue
			}
			if ic.incomingPub != nil {
				outPublish <- ic.incomingPub
				continue
			}
			ERROR.Println("startComms received empty incomingComms msg")
		}
		close(outboundFromIncoming)
		close(outPublish)
		wg.Done()
	}()

	go func() {
		for err := range oboundErr {
			outError <- err
		}
		wg.Done()
	}()

	go func() {
		wg.Wait()
		close(outError)
		DEBUG.Println(NET, "startComms closing outError")
	}()

	return outPublish, outError
}

type incomingComms struct {
	err         error
	outbound    *PacketAndToken
	incomingPub *codec.PushPacket
}

type inbound struct {
	err error
	cp  codec.ControlPacket
}

func startIncoming(conn io.ReadWriter) <-chan inbound {
	var err error
	var cp codec.ControlPacket
	ibound := make(chan inbound)

	DEBUG.Println(NET, "incoming started")

	go func() {
		for {
			if cp, err = codec.ReadPacket(conn); err != nil {
				if strings.Contains(err.Error(), "unsupported packet type") {
					dm := codec.NewControlPacket(codec.Disconnect).(*codec.DisconnectPacket)
					dm.SetReturnCode(codec.DiscProtocolViolation)
					_ = dm.Write(conn)
				}
				if !strings.Contains(err.Error(), closedNetConnErrorText) {
					ibound <- inbound{err: err}
				}
				close(ibound)
				DEBUG.Println(NET, "incoming complete")
				return
			}
			DEBUG.Println(NET, "startIncoming Received Message")
			ibound <- inbound{cp: cp}
		}
	}()

	return ibound
}

func startIncomingComms(conn io.ReadWriter,
	c commsFns,
	inboundFromStore <-chan codec.ControlPacket,
) <-chan incomingComms {
	ibound := startIncoming(conn)
	output := make(chan incomingComms)

	DEBUG.Println(NET, "startIncomingComms started")
	go func() {
		for {
			if inboundFromStore == nil && ibound == nil {
				close(output)
				DEBUG.Println(NET, "startIncomingComms goroutine complete")
				return
			}
			DEBUG.Println(NET, "logic waiting for msg on ibound")

			var msg codec.ControlPacket
			var ok bool
			select {
			case msg, ok = <-inboundFromStore:
				if !ok {
					DEBUG.Println(NET, "startIncomingComms: inboundFromStore complete")
					inboundFromStore = nil
					continue
				}
				DEBUG.Println(NET, "startIncomingComms: got msg from store")
			case ibMsg, ok := <-ibound:
				if !ok {
					DEBUG.Println(NET, "startIncomingComms: ibound complete")
					ibound = nil
					continue
				}
				DEBUG.Println(NET, "startIncomingComms: got msg on ibound")
				if ibMsg.err != nil {
					output <- incomingComms{err: ibMsg.err}
					continue
				}
				msg = ibMsg.cp
				c.UpdateLastReceived()
			}

			switch m := msg.(type) {
			case *codec.PushPacket:
				DEBUG.Println(NET, "startIncomingComms: received publish")
				output <- incomingComms{incomingPub: m}
			case *codec.PingreqPacket:
				DEBUG.Println(NET, "receive heartbeat")
			case *codec.PingrespPacket:
				DEBUG.Println(NET, "PINGRESP received", time.Now())
			case *codec.DisconnectPacket:
				c.CloseConnect(m.GetReturnCode())
			}
		}
	}()
	return output
}

func startOutgoingComms(conn net.Conn,
	c commsFns,
	oboundp <-chan *PacketAndToken,
	obound <-chan *PacketAndToken,
	oboundFromIncoming <-chan *PacketAndToken,
) <-chan error {
	errChan := make(chan error)
	DEBUG.Println(NET, "outgoing started")

	go func() {
		for {
			DEBUG.Println(NET, "outgoing waiting for an outbound message")

			if oboundp == nil && obound == nil && oboundFromIncoming == nil {
				DEBUG.Println(NET, "outgoing comms stopping")
				close(errChan)
				return
			}

			select {
			case pub, ok := <-obound:
				if !ok {
					obound = nil
					continue
				}
				msg := pub.p.(*codec.PushPacket)
				DEBUG.Println(NET, "obound msg to write")

				writeTimeout := c.getWriteTimeOut()
				if writeTimeout > 0 {
					if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
						ERROR.Println(NET, "SetWriteDeadline ", err)
					}
				}

				if err := msg.Write(conn); err != nil {
					ERROR.Println(NET, "outgoing obound reporting error ", err)
					pub.t.setError(err)
					if !strings.Contains(err.Error(), closedNetConnErrorText) {
						errChan <- err
					}
					continue
				}

				if writeTimeout > 0 {
					if err := conn.SetWriteDeadline(time.Time{}); err != nil {
						ERROR.Println(NET, "SetWriteDeadline to 0 ", err)
					}
				}

				pub.t.flowComplete()
				DEBUG.Println(NET, "obound wrote msg")
			case msg, ok := <-oboundp:
				if !ok {
					oboundp = nil
					continue
				}
				DEBUG.Println(NET, "obound priority msg to write, type", reflect.TypeOf(msg.p))
				if err := msg.p.Write(conn); err != nil {
					ERROR.Println(NET, "outgoing oboundp reporting error ", err)
					if msg.t != nil {
						msg.t.setError(err)
					}
					errChan <- err
					continue
				}

				if _, ok := msg.p.(*codec.DisconnectPacket); ok {
					msg.t.(*DisconnectToken).flowComplete()
					DEBUG.Println(NET, "outbound wrote disconnect, closing connection")
					_ = conn.Close()
				}
			case msg, ok := <-oboundFromIncoming:
				if !ok {
					oboundFromIncoming = nil
					continue
				}
				DEBUG.Println(NET, "obound from incoming msg to write, type", reflect.TypeOf(msg.p))
				if err := msg.p.Write(conn); err != nil {
					ERROR.Println(NET, "outgoing oboundFromIncoming reporting error", err)
					if msg.t != nil {
						msg.t.setError(err)
					}
					errChan <- err
					continue
				}
			}
			c.UpdateLastSent()
		}
	}()
	return errChan
}
