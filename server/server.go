package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
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
	conn.SendDisconnect(reason)
	conn.Close()
	_ = s.store.Remove(deviceID, conn)
	s.options.ConnectionListener.OnConnectionClosed(deviceID, fmt.Sprintf("kicked with reason 0x%02x", reason))
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

	if connectPkt.MagicNumber != 0x637a7175 {
		conn.Close()
		return
	}

	if connectPkt.ProtocolVersion != 1 {
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

	dc := newDeviceConnection(conn, deviceID)
	if prev := s.store.Register(deviceID, dc); prev != nil {
		// Tear down the superseded connection asynchronously so a slow/stuck
		// old client cannot delay the new connection that just won the
		// takeover. SendDisconnect is bounded by writeTimeout; Close then
		// unblocks the old connection's read loop. prev is already replaced
		// in the store, so this goroutine owns its teardown.
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

func (s *serverImpl) handleDeviceConnection(dc *deviceConnection, serverKp int64) {
	defer func() {
		dc.Close()
		s.store.Remove(dc.DeviceID(), dc)
		s.options.ConnectionListener.OnConnectionClosed(dc.DeviceID(), "connection closed")
		INFO.Printf("device %s disconnected", dc.DeviceID())
	}()

	conn := dc.conn

	var keepaliveTimer *time.Timer
	if serverKp > 0 {
		timeout := time.Duration(float64(serverKp) * float64(time.Second) * 1.5)
		keepaliveTimer = time.AfterFunc(timeout, func() {
			WARN.Printf("keepalive timeout reaping device %s (no packet for %v, serverKp=%ds)",
				dc.DeviceID(), timeout, serverKp)
			dc.SendDisconnect(codec.DiscKeepaliveTimeout)
			dc.Close()
		})
		defer keepaliveTimer.Stop()
	}

	for {
		cp, err := codec.ReadPacket(conn)
		if err != nil {
			// An unrecognised packet type is a protocol violation; reply DISCONNECT(0x04)
			if strings.Contains(err.Error(), "unsupported packet type") {
				dp := codec.NewControlPacket(codec.Disconnect).(*codec.DisconnectPacket)
				dp.SetReturnCode(codec.DiscProtocolViolation)
				_ = dp.Write(conn)
			}
			if err == io.EOF || errors.Is(err, net.ErrClosed) || strings.Contains(err.Error(), "use of closed network connection") {
				return
			}
			return
		}

		if keepaliveTimer != nil && serverKp > 0 {
			timeout := time.Duration(float64(serverKp) * float64(time.Second) * 1.5)
			keepaliveTimer.Reset(timeout)
		}

		switch pkt := cp.(type) {
		case *codec.PushPacket:
			if s.options.MessageHandler != nil {
				go s.options.MessageHandler(dc.DeviceID(), pkt.Payload)
			}
		case *codec.PingreqPacket:
			DEBUG.Printf("PINGREQ from device=%s -> PINGRESP", dc.DeviceID())
			pr := codec.NewControlPacket(codec.Pingresp).(*codec.PingrespPacket)
			_ = pr.Write(conn)
		case *codec.DisconnectPacket:
			return
		default:
			dp := codec.NewControlPacket(codec.Disconnect).(*codec.DisconnectPacket)
			dp.SetReturnCode(codec.DiscProtocolViolation)
			_ = dp.Write(conn)
			return
		}
	}
}
