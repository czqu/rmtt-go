package client

import (
	"errors"
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
	_ = h.dispatch(messages, &client{})

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
