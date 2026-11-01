package client

import (
	"errors"
	"io"
	"net"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/czqu/rmtt-go/codec"
)

func TestClient_IsConnected_Initial(t *testing.T) {
	c := NewClient(NewClientOptions())
	if c.IsConnected() {
		t.Error("IsConnected() = true before connect")
	}
}

func TestClient_Push_NotConnected(t *testing.T) {
	c := NewClient(NewClientOptions())
	tok := c.Push("hello")
	if !errors.Is(tok.Error(), ErrNotConnected) {
		t.Errorf("Push() error = %v, want ErrNotConnected", tok.Error())
	}
}

func TestClient_Push_UnknownPayloadType(t *testing.T) {
	c := NewClient(NewClientOptions()).(*client)
	c.status.forceConnectionStatus(connected)
	tok := c.Push(42)
	if tok.Error() == nil {
		t.Error("Push(42) error = nil, want unknown payload type error")
	}
}

func TestClient_Connect_NoServers(t *testing.T) {
	o := NewClientOptions()
	o.Servers = nil
	c := NewClient(o)
	tok := c.Connect()
	if !tok.WaitTimeout(3 * time.Second) {
		t.Fatal("Connect() token did not complete")
	}
	if tok.Error() == nil {
		t.Error("Connect() error = nil, want 'no server to connect to'")
	}
}

func TestNewConnectMsgFromOptions(t *testing.T) {
	o := NewClientOptions()
	o.SetCredential("dev-001")
	o.Heartbeat = 10
	u, _ := url.Parse("tcp://127.0.0.1:18883")

	cm := newConnectMsgFromOptions(o, u)
	if cm.MagicNumber != 0x637a7175 {
		t.Errorf("MagicNumber = 0x%x, want 0x637a7175", cm.MagicNumber)
	}
	if cm.ProtocolVersion != byte(o.ProtocolVersion) {
		t.Errorf("ProtocolVersion = %d, want %d", cm.ProtocolVersion, o.ProtocolVersion)
	}
	if cm.Credential != "dev-001" {
		t.Errorf("Credential = %q, want dev-001", cm.Credential)
	}
	if cm.Keepalive != 10 {
		t.Errorf("Keepalive = %d, want 10", cm.Keepalive)
	}
}

func TestNewConnectMsgFromOptions_Adaptive(t *testing.T) {
	o := NewClientOptions()
	o.SetAdaptiveHeartbeat(10, 300)
	u, _ := url.Parse("tcp://127.0.0.1:18883")

	cm := newConnectMsgFromOptions(o, u)
	if cm.Keepalive != 300 {
		t.Errorf("Keepalive = %d, want 300 (AdaptiveMax)", cm.Keepalive)
	}
}

func TestHandlerDispatch(t *testing.T) {
	h := newHandler()
	expected := "payload-1"

	var mu sync.Mutex
	count := 0
	h.AddLast(func(_ Client, m Message) {
		mu.Lock()
		defer mu.Unlock()
		if string(m.Payload()) != expected {
			t.Errorf("handler payload = %q, want %q", m.Payload(), expected)
		}
		count++
	})
	h.AddLast(func(_ Client, _ Message) {
		mu.Lock()
		defer mu.Unlock()
		count++
	})

	messages := make(chan *codec.PushPacket)
	go h.dispatch(messages, &client{})

	messages <- &codec.PushPacket{Payload: []byte(expected)}
	close(messages)

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		cnt := count
		mu.Unlock()
		if cnt == 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("handlers not all invoked, count = %d", cnt)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestCloseConnect_AdminDisconnectReportsLoss asserts that a server-initiated
// DISCONNECT 0x02 (session takeover) or 0x06 (kicked by admin) fires
// OnConnectionLost with a reason-bearing error and does NOT auto-reconnect —
// reconnecting would evict the connection that took over, or get kicked again.
// These codes used to go through Disconnect(100), so OnConnectionLost never
// fired (e2e blackbox F12).
func TestCloseConnect_AdminDisconnectReportsLoss(t *testing.T) {
	tests := []struct {
		name    string
		reason  byte
		wantErr error
	}{
		{"session takeover", codec.DiscSessionTakenOver, ErrSessionTakenOver},
		{"kicked by admin", codec.DiscKickedByAdmin, ErrKickedByAdmin},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			defer ln.Close()

			serverDone := make(chan error, 1)
			go func() {
				conn, err := ln.Accept()
				if err != nil {
					serverDone <- err
					return
				}
				defer conn.Close()
				if _, err := codec.ReadPacket(conn); err != nil { // CONNECT
					serverDone <- err
					return
				}
				ca := codec.NewControlPacket(codec.Connack).(*codec.ConnackPacket)
				ca.ReturnCode = codec.Accepted
				ca.ServerKeepalive = 0
				if err := ca.Write(conn); err != nil {
					serverDone <- err
					return
				}
				dp := codec.NewControlPacket(codec.Disconnect).(*codec.DisconnectPacket)
				dp.SetReturnCode(tt.reason)
				if err := dp.Write(conn); err != nil {
					serverDone <- err
					return
				}
				// The client must close the conn cleanly (EOF, not RST).
				_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
				_, err = conn.Read(make([]byte, 1))
				serverDone <- err
			}()

			lost := make(chan error, 4)
			o := NewClientOptions()
			o.AddServer("tcp://" + ln.Addr().String())
			o.SetCredential("dev-admin")
			o.Heartbeat = 0 // no keepalive workers in this test
			o.ConnectRetry = false
			o.AutoReconnect = true // must still NOT reconnect after 0x02/0x06
			o.SetReconnectBase(10 * time.Millisecond)
			o.SetConnectTimeout(3 * time.Second)
			o.OnConnectionLost = func(_ Client, err error) { lost <- err }

			c := NewClient(o)
			tok := c.Connect()
			if !tok.WaitTimeout(3 * time.Second) {
				t.Fatal("Connect() token did not complete")
			}
			if tok.Error() != nil {
				t.Fatalf("Connect() error = %v", tok.Error())
			}

			select {
			case err := <-lost:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("OnConnectionLost error = %v, want %v", err, tt.wantErr)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("OnConnectionLost was not called for a session-terminating DISCONNECT")
			}
			if c.IsConnected() {
				t.Error("IsConnected() = true after session-terminating DISCONNECT")
			}

			// No auto-reconnect: nobody may dial back in.
			_ = ln.(*net.TCPListener).SetDeadline(time.Now().Add(400 * time.Millisecond))
			if conn2, err := ln.Accept(); err == nil {
				conn2.Close()
				t.Fatal("client reconnected after a session-terminating DISCONNECT")
			}

			// Exactly one OnConnectionLost call.
			select {
			case err := <-lost:
				t.Fatalf("OnConnectionLost fired a second time with %v", err)
			default:
			}

			if err := <-serverDone; err != nil && !errors.Is(err, io.EOF) {
				t.Errorf("final server read error = %v, want EOF (clean client close)", err)
			}
		})
	}
}
