package server

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/czqu/rmtt-go/codec"
)

type fakeAuth struct{}

func (fakeAuth) Authenticate(credential string) (string, bool) {
	return credential, true
}

type rejectAuth struct{}

func (rejectAuth) Authenticate(string) (string, bool) { return "", false }

type fakeConnListener struct{}

func (fakeConnListener) OnConnectionEstablished(string)    {}
func (fakeConnListener) OnConnectionClosed(string, string) {}

func newTestServer(opts *ServerOptions) *serverImpl {
	if opts == nil {
		opts = NewServerOptions()
	}
	if opts.KeepalivePolicy == nil {
		opts.KeepalivePolicy = DefaultKeepalivePolicy()
	}
	if opts.ConnectionListener == nil {
		opts.ConnectionListener = noopConnectionListener{}
	}
	return &serverImpl{
		options: opts,
		store:   NewConnectionStore(),
		conns:   make(map[net.Conn]struct{}),
	}
}

func readConnack(t testing.TB, c net.Conn) *codec.ConnackPacket {
	t.Helper()
	cp, err := codec.ReadPacket(c)
	if err != nil {
		t.Fatalf("CONNACK read error: %v", err)
	}
	ca, ok := cp.(*codec.ConnackPacket)
	if !ok {
		t.Fatalf("got %T, want *codec.ConnackPacket", cp)
	}
	return ca
}

// waitRegistered polls the store until deviceID is registered, failing the
// test after a short timeout. The server sends CONNACK before registering the
// connection, so reading CONNACK alone does not guarantee the device is in the
// store yet — session-takeover tests need the first connection registered
// before opening the second, otherwise the second CONNECT can race ahead and
// register first, sending the takeover DISCONNECT to the wrong client end.
func waitRegistered(t testing.TB, srv *serverImpl, deviceID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, ok := srv.store.Get(deviceID); ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("device %s did not register within 2s", deviceID)
		}
		time.Sleep(time.Millisecond)
	}
}

func sendConnect(c net.Conn, magic uint32, version byte, credential string) error {
	cm := codec.NewControlPacket(codec.Connect).(*codec.ConnectPacket)
	cm.MagicNumber = magic
	cm.ProtocolVersion = version
	cm.Credential = credential
	return cm.Write(c)
}

func TestServer_HandleConnection_Success(t *testing.T) {
	var mu sync.Mutex
	var received []string
	opts := NewServerOptions()
	opts.SetMessageHandler(func(deviceID string, payload []byte) {
		mu.Lock()
		defer mu.Unlock()
		received = append(received, deviceID+":"+string(payload))
	})
	srv := newTestServer(opts)

	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()

	done := make(chan struct{})
	go func() {
		srv.handleConnection(serverSide)
		close(done)
	}()

	cm := codec.NewControlPacket(codec.Connect).(*codec.ConnectPacket)
	cm.MagicNumber = 0x637a7175
	cm.ProtocolVersion = 1
	cm.Credential = "dev-001"
	cm.Keepalive = 120
	if err := cm.Write(clientSide); err != nil {
		t.Fatalf("CONNECT write error: %v", err)
	}

	ca := readConnack(t, clientSide)
	if ca.ReturnCode != codec.Accepted {
		t.Fatalf("ReturnCode = 0x%x, want Accepted", ca.ReturnCode)
	}
	if ca.ServerKeepalive != 120 {
		t.Errorf("ServerKeepalive = %d, want 120", ca.ServerKeepalive)
	}

	// uplink PUSH routed to the message handler
	pp := codec.NewControlPacket(codec.Push).(*codec.PushPacket)
	pp.Payload = []byte("hello")
	if err := pp.Write(clientSide); err != nil {
		t.Fatalf("uplink PUSH write error: %v", err)
	}

	// downlink push from the server; read it in a goroutine so the write
	// completes
	downCh := make(chan *codec.PushPacket, 1)
	downErr := make(chan error, 1)
	go func() {
		cp, err := codec.ReadPacket(clientSide)
		if err != nil {
			downErr <- err
			return
		}
		p, ok := cp.(*codec.PushPacket)
		if !ok {
			downErr <- errors.New("downlink packet is not a PUSH")
			return
		}
		downCh <- p
	}()
	if err := srv.Push("dev-001", []byte("down")); err != nil {
		t.Fatalf("Push() error: %v", err)
	}
	select {
	case p := <-downCh:
		if string(p.Payload) != "down" {
			t.Errorf("downlink payload = %q, want down", p.Payload)
		}
	case err := <-downErr:
		t.Fatalf("downlink read error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for downlink PUSH")
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(received)
		mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("message handler not invoked")
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	if received[0] != "dev-001:hello" {
		t.Errorf("handler received %q, want dev-001:hello", received[0])
	}
	mu.Unlock()

	dm := codec.NewControlPacket(codec.Disconnect).(*codec.DisconnectPacket)
	if err := dm.Write(clientSide); err != nil {
		t.Fatalf("DISCONNECT write error: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleConnection did not return after DISCONNECT")
	}
	if _, ok := srv.store.Get("dev-001"); ok {
		t.Error("device still registered after disconnect")
	}
}

func TestServer_HandleConnection_BadMagic(t *testing.T) {
	srv := newTestServer(NewServerOptions())
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()

	go srv.handleConnection(serverSide)

	if err := sendConnect(clientSide, 0xdeadbeef, 1, "dev"); err != nil {
		t.Fatalf("CONNECT write error: %v", err)
	}
	// server must close the connection without replying
	if _, err := codec.ReadPacket(clientSide); err == nil {
		t.Error("expected connection close, got a packet")
	}
}

func TestServer_HandleConnection_BadProtocolVersion(t *testing.T) {
	srv := newTestServer(NewServerOptions())
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()

	go srv.handleConnection(serverSide)

	if err := sendConnect(clientSide, 0x637a7175, 2, "dev"); err != nil {
		t.Fatalf("CONNECT write error: %v", err)
	}
	ca := readConnack(t, clientSide)
	if ca.ReturnCode != codec.ErrRefusedBadProtocolVersion {
		t.Errorf("ReturnCode = 0x%x, want ErrRefusedBadProtocolVersion", ca.ReturnCode)
	}
}

func TestServer_HandleConnection_AuthRejected(t *testing.T) {
	opts := NewServerOptions()
	opts.SetAuthenticator(rejectAuth{})
	srv := newTestServer(opts)
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()

	go srv.handleConnection(serverSide)

	if err := sendConnect(clientSide, 0x637a7175, 1, "dev"); err != nil {
		t.Fatalf("CONNECT write error: %v", err)
	}
	ca := readConnack(t, clientSide)
	if ca.ReturnCode != codec.ErrRefusedNotAuthorised {
		t.Errorf("ReturnCode = 0x%x, want ErrRefusedNotAuthorised", ca.ReturnCode)
	}
}

func TestServer_HandleConnection_EmptyDeviceID(t *testing.T) {
	srv := newTestServer(NewServerOptions())
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()

	go srv.handleConnection(serverSide)

	if err := sendConnect(clientSide, 0x637a7175, 1, ""); err != nil {
		t.Fatalf("CONNECT write error: %v", err)
	}
	ca := readConnack(t, clientSide)
	if ca.ReturnCode != codec.ErrRefusedNotAuthorised {
		t.Errorf("ReturnCode = 0x%x, want ErrRefusedNotAuthorised", ca.ReturnCode)
	}
}

func TestServer_HandleConnection_SessionTakeover(t *testing.T) {
	srv := newTestServer(NewServerOptions())

	serverSide1, clientSide1 := net.Pipe()
	defer serverSide1.Close()
	defer clientSide1.Close()
	serverSide2, clientSide2 := net.Pipe()
	defer serverSide2.Close()
	defer clientSide2.Close()

	done1 := make(chan struct{})
	done2 := make(chan struct{})
	go func() {
		srv.handleConnection(serverSide1)
		close(done1)
	}()
	go func() {
		srv.handleConnection(serverSide2)
		close(done2)
	}()

	if err := sendConnect(clientSide1, 0x637a7175, 1, "dev"); err != nil {
		t.Fatalf("first CONNECT write error: %v", err)
	}
	if ca := readConnack(t, clientSide1); ca.ReturnCode != codec.Accepted {
		t.Fatalf("first CONNACK ReturnCode = 0x%x, want Accepted", ca.ReturnCode)
	}

	// Ensure the first connection is registered before opening the second, so
	// the takeover target is deterministic.
	waitRegistered(t, srv, "dev")

	if err := sendConnect(clientSide2, 0x637a7175, 1, "dev"); err != nil {
		t.Fatalf("second CONNECT write error: %v", err)
	}
	if ca := readConnack(t, clientSide2); ca.ReturnCode != codec.Accepted {
		t.Fatalf("second CONNACK ReturnCode = 0x%x, want Accepted", ca.ReturnCode)
	}

	// the first connection must receive DISCONNECT (session taken over)
	cp, err := codec.ReadPacket(clientSide1)
	if err != nil {
		t.Fatalf("first connection DISCONNECT read error: %v", err)
	}
	dp, ok := cp.(*codec.DisconnectPacket)
	if !ok || dp.GetReturnCode() != codec.DiscSessionTakenOver {
		t.Errorf("first connection got %v, want DISCONNECT with DiscSessionTakenOver", cp)
	}

	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatal("first handleConnection did not return after takeover")
	}

	// the newest connection stays registered
	if _, ok := srv.store.Get("dev"); !ok {
		t.Error("newest connection not registered after takeover")
	}

	dm := codec.NewControlPacket(codec.Disconnect).(*codec.DisconnectPacket)
	if err := dm.Write(clientSide2); err != nil {
		t.Fatalf("DISCONNECT write error: %v", err)
	}
	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatal("second handleConnection did not return after DISCONNECT")
	}
}

func TestServer_HandleDeviceConnection_PingPong(t *testing.T) {
	srv := newTestServer(NewServerOptions())
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()

	done := make(chan struct{})
	go func() {
		srv.handleConnection(serverSide)
		close(done)
	}()

	if err := sendConnect(clientSide, 0x637a7175, 1, "dev"); err != nil {
		t.Fatalf("CONNECT write error: %v", err)
	}
	if ca := readConnack(t, clientSide); ca.ReturnCode != codec.Accepted {
		t.Fatalf("CONNACK ReturnCode = 0x%x, want Accepted", ca.ReturnCode)
	}

	pr := codec.NewControlPacket(codec.Pingreq).(*codec.PingreqPacket)
	if err := pr.Write(clientSide); err != nil {
		t.Fatalf("PINGREQ write error: %v", err)
	}
	cp, err := codec.ReadPacket(clientSide)
	if err != nil {
		t.Fatalf("PINGRESP read error: %v", err)
	}
	if _, ok := cp.(*codec.PingrespPacket); !ok {
		t.Errorf("got %T, want *codec.PingrespPacket", cp)
	}

	dm := codec.NewControlPacket(codec.Disconnect).(*codec.DisconnectPacket)
	if err := dm.Write(clientSide); err != nil {
		t.Fatalf("DISCONNECT write error: %v", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleConnection did not return after DISCONNECT")
	}
}

func TestServer_HandleDeviceConnection_ProtocolViolation(t *testing.T) {
	srv := newTestServer(NewServerOptions())
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()

	done := make(chan struct{})
	go func() {
		srv.handleConnection(serverSide)
		close(done)
	}()

	if err := sendConnect(clientSide, 0x637a7175, 1, "dev"); err != nil {
		t.Fatalf("CONNECT write error: %v", err)
	}
	if ca := readConnack(t, clientSide); ca.ReturnCode != codec.Accepted {
		t.Fatalf("CONNACK ReturnCode = 0x%x, want Accepted", ca.ReturnCode)
	}

	// a client sending CONNACK is a protocol violation
	ca := codec.NewControlPacket(codec.Connack).(*codec.ConnackPacket)
	ca.ReturnCode = codec.Accepted
	if err := ca.Write(clientSide); err != nil {
		t.Fatalf("CONNACK write error: %v", err)
	}
	cp, err := codec.ReadPacket(clientSide)
	if err != nil {
		t.Fatalf("DISCONNECT read error: %v", err)
	}
	dp, ok := cp.(*codec.DisconnectPacket)
	if !ok || dp.GetReturnCode() != codec.DiscProtocolViolation {
		t.Errorf("got %v, want DISCONNECT with DiscProtocolViolation", cp)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handleConnection did not return after protocol violation")
	}
}
