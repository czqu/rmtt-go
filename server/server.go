package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/czqu/rmtt-go/codec"
)

// Server is an rmtt server that accepts device connections over its
// registered listeners, authenticates devices and routes messages.
type Server interface {
	ListenAndServe() error
	ListenAndServeContext(ctx context.Context) error
	Push(deviceID string, payload []byte) error
	Kick(deviceID string, reason byte) error
	Close() error
}

type serverImpl struct {
	options   *ServerOptions
	store     *ConnectionStore
	listeners []Listener
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup

	connMu sync.Mutex
	conns  map[net.Conn]struct{}
}

// NewServer creates a Server from the supplied options. A nil options value
// uses NewServerOptions defaults; when no listener was added via AddListener,
// a single TCP listener on options.Port is used.
func NewServer(opts *ServerOptions) Server {
	if opts == nil {
		opts = NewServerOptions()
	}
	if opts.KeepalivePolicy == nil {
		opts.KeepalivePolicy = DefaultKeepalivePolicy()
	}
	if opts.HandshakeTimeout <= 0 {
		opts.HandshakeTimeout = 10 * time.Second
	}
	if opts.ConnectionListener == nil {
		opts.ConnectionListener = noopConnectionListener{}
	}
	listeners := opts.listeners
	if len(listeners) == 0 {
		listeners = []Listener{NewTCPListener(fmt.Sprintf(":%d", opts.Port))}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &serverImpl{
		options:   opts,
		store:     NewConnectionStore(),
		listeners: listeners,
		ctx:       ctx,
		cancel:    cancel,
		conns:     make(map[net.Conn]struct{}),
	}
	return s
}

func (s *serverImpl) ListenAndServe() error {
	return s.ListenAndServeContext(context.Background())
}

func (s *serverImpl) ListenAndServeContext(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)
	defer s.cancel()

	errCh := make(chan error, len(s.listeners))
	for _, l := range s.listeners {
		l := l
		go func() {
			INFO.Printf("rmtt server (go) listener starting: %T", l)
			if err := l.Serve(s.ctx, s.handleConnection); err != nil {
				select {
				case errCh <- err:
				default:
				}
			}
		}()
	}

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *serverImpl) Push(deviceID string, payload []byte) error {
	conn, ok := s.store.Get(deviceID)
	if !ok || !conn.IsActive() {
		return fmt.Errorf("device %s not connected", deviceID)
	}
	return conn.Write(payload)
}

func (s *serverImpl) Kick(deviceID string, reason byte) error {
	conn, ok := s.store.Get(deviceID)
	if !ok || !conn.IsActive() {
		return fmt.Errorf("device %s not connected", deviceID)
	}
	reasonText := fmt.Sprintf("kicked with reason 0x%02x", reason)
	// Record the reason on the connection so whichever teardown wins the
	// store race (this Kick or the connection's own read-loop defer) reports
	// it; the Remove gate below guarantees exactly one OnConnectionClosed.
	if dc, ok := conn.(*deviceConnection); ok {
		dc.setCloseReason(reasonText)
	}
	conn.SendDisconnect(reason)
	conn.Close()
	if s.store.Remove(deviceID, conn) {
		s.options.ConnectionListener.OnConnectionClosed(deviceID, reasonText)
	}
	return nil
}

func (s *serverImpl) Close() error {
	s.cancel()
	for _, l := range s.listeners {
		_ = l.Close()
	}
	for _, conn := range s.store.All() {
		conn.SendDisconnect(codec.DiscServerShutdown)
		conn.Close()
	}
	s.connMu.Lock()
	for conn := range s.conns {
		conn.Close()
	}
	s.connMu.Unlock()
	s.wg.Wait()
	return nil
}

func (s *serverImpl) handleConnection(conn net.Conn) {
	s.wg.Add(1)
	defer s.wg.Done()

	s.connMu.Lock()
	s.conns[conn] = struct{}{}
	s.connMu.Unlock()
	defer func() {
		s.connMu.Lock()
		delete(s.conns, conn)
		s.connMu.Unlock()
	}()

	// Bound the CONNECT handshake: a peer that connects and sends nothing
	// must not hold a goroutine and fd forever.
	if s.options.HandshakeTimeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(s.options.HandshakeTimeout))
	}

	cp, err := codec.ReadPacket(conn)
	if err != nil {
		conn.Close()
		return
	}

	connectPkt, ok := cp.(*codec.ConnectPacket)
	if !ok {
		conn.Close()
		return
	}

	if connectPkt.MagicNumber != codec.MagicNumber {
		conn.Close()
		return
	}

	if connectPkt.ProtocolVersion != codec.ProtocolVersion {
		ca := codec.NewControlPacket(codec.Connack).(*codec.ConnackPacket)
		ca.ReturnCode = codec.ErrRefusedBadProtocolVersion
		ca.Write(conn)
		conn.Close()
		return
	}

	var deviceID string
	var allowed bool
	if s.options.Authenticator != nil {
		deviceID, allowed = s.options.Authenticator.Authenticate(connectPkt.Credential)
		if !allowed {
			ca := codec.NewControlPacket(codec.Connack).(*codec.ConnackPacket)
			ca.ReturnCode = codec.ErrRefusedNotAuthorised
			ca.Write(conn)
			conn.Close()
			return
		}
	} else {
		deviceID = connectPkt.Credential
	}

	if deviceID == "" {
		ca := codec.NewControlPacket(codec.Connack).(*codec.ConnackPacket)
		ca.ReturnCode = codec.ErrRefusedNotAuthorised
		ca.Write(conn)
		conn.Close()
		return
	}

	serverKp := s.options.KeepalivePolicy.Decide(int64(connectPkt.Keepalive))

	ca := codec.NewControlPacket(codec.Connack).(*codec.ConnackPacket)
	ca.ReturnCode = codec.Accepted
	ca.ServerKeepalive = uint16(serverKp)
	if err := ca.Write(conn); err != nil {
		conn.Close()
		return
	}

	// Handshake complete: hand deadline control to the keepalive policy.
	_ = conn.SetDeadline(time.Time{})

	dc := newDeviceConnection(conn, deviceID)
	if prev := s.store.Register(deviceID, dc); prev != nil {
		// The superseded connection is already replaced in the store, so its
		// teardown defer will not report the closed event — report it here,
		// before the new connection's established event, so consumers observe
		// a strict closed-then-established order per device. The teardown
		// itself is asynchronous so a slow/stuck old client cannot delay the
		// new connection that just won the takeover.
		s.options.ConnectionListener.OnConnectionClosed(deviceID, "session taken over")
		go func(prev DeviceConnection) {
			prev.SendDisconnect(codec.DiscSessionTakenOver)
			prev.Close()
		}(prev)
	}

	s.options.ConnectionListener.OnConnectionEstablished(deviceID)
	INFO.Printf("device %s connected (proposal=%ds serverKp=%ds)",
		deviceID, connectPkt.Keepalive, serverKp)

	s.handleDeviceConnection(dc, serverKp)
}

// pushQueueSize bounds the per-connection backlog of uplink PUSH payloads
// waiting for the MessageHandler before the read loop applies backpressure.
const pushQueueSize = 64

func (s *serverImpl) handleDeviceConnection(dc *deviceConnection, serverKp int64) {
	defer func() {
		dc.Close()
		// Single outlet for the connection-closed event: the Remove gate
		// makes it fire exactly once even when an external actor (Kick) tears
		// the connection down concurrently.
		if s.store.Remove(dc.DeviceID(), dc) {
			s.options.ConnectionListener.OnConnectionClosed(dc.DeviceID(), dc.reason())
		}
		INFO.Printf("device %s disconnected", dc.DeviceID())
	}()

	conn := dc.conn

	// Per-connection ordered dispatch: uplink PUSHes must reach the
	// MessageHandler in the order they were received, pipelined frames in a
	// single TCP segment included. One worker per connection fed by a bounded
	// queue keeps that order without letting a slow handler stall the read
	// loop (and with it PINGRESP and keepalive resets); when the queue is full
	// the read loop blocks, i.e. TCP backpressure instead of message loss.
	var pushQueue chan []byte
	if handler := s.options.MessageHandler; handler != nil {
		pushQueue = make(chan []byte, pushQueueSize)
		go func() {
			for payload := range pushQueue {
				handler(dc.DeviceID(), payload)
			}
		}()
		defer close(pushQueue)
	}

	var keepaliveTimer *time.Timer
	if serverKp > 0 {
		timeout := time.Duration(float64(serverKp) * float64(time.Second) * 1.5)
		keepaliveTimer = time.AfterFunc(timeout, func() {
			WARN.Printf("keepalive timeout reaping device %s (no packet for %v, serverKp=%ds)",
				dc.DeviceID(), timeout, serverKp)
			dc.setCloseReason("keepalive timeout")
			dc.SendDisconnect(codec.DiscKeepaliveTimeout)
			dc.Close()
		})
		defer keepaliveTimer.Stop()
	}

	for {
		cp, err := codec.ReadPacket(conn)
		if err != nil {
			// An unrecognised packet type or non-zero fixed-header flags is a
			// protocol violation; reply DISCONNECT(0x04). The codec consumes
			// the rejected frame first, so this reply reaches the peer before
			// the close instead of being lost to a TCP reset.
			if errors.Is(err, codec.ErrUnsupportedPacketType) || errors.Is(err, codec.ErrInvalidFixedHeaderFlags) {
				dp := codec.NewControlPacket(codec.Disconnect).(*codec.DisconnectPacket)
				dp.SetReturnCode(codec.DiscProtocolViolation)
				_ = dc.writeControl(dp)
			}
			return
		}

		if keepaliveTimer != nil && serverKp > 0 {
			timeout := time.Duration(float64(serverKp) * float64(time.Second) * 1.5)
			keepaliveTimer.Reset(timeout)
		}

		switch pkt := cp.(type) {
		case *codec.PushPacket:
			if pushQueue != nil {
				pushQueue <- pkt.Payload
			}
		case *codec.PingreqPacket:
			DEBUG.Printf("PINGREQ from device=%s -> PINGRESP", dc.DeviceID())
			pr := codec.NewControlPacket(codec.Pingresp).(*codec.PingrespPacket)
			_ = dc.writeControl(pr)
		case *codec.DisconnectPacket:
			return
		default:
			dp := codec.NewControlPacket(codec.Disconnect).(*codec.DisconnectPacket)
			dp.SetReturnCode(codec.DiscProtocolViolation)
			_ = dc.writeControl(dp)
			return
		}
	}
}
