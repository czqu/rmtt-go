package server

// Regression tests for connection-lifecycle event semantics: exactly one
// OnConnectionClosed per established connection, and per-device
// closed-then-established ordering across session takeovers.

import (
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/czqu/rmtt-go/codec"
)

type eventRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *eventRecorder) OnConnectionEstablished(deviceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "established("+deviceID+")")
}

func (r *eventRecorder) OnConnectionClosed(deviceID string, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, "closed("+deviceID+", \""+reason+"\")")
}

func (r *eventRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

// TestLifecycle_SessionTakeoverEventOrder asserts that a session takeover
// reports the superseded connection's closed event BEFORE the new
// connection's established event, and that the superseded connection's own
// teardown does not emit a second closed event.
func TestLifecycle_SessionTakeoverEventOrder(t *testing.T) {
	rec := &eventRecorder{}
	opts := NewServerOptions()
	opts.SetConnectionListener(rec)
	srv := newTestServer(opts)

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

	if err := sendConnect(clientSide1, codec.MagicNumber, codec.ProtocolVersion, "dev"); err != nil {
		t.Fatalf("first CONNECT write error: %v", err)
	}
	if ca := readConnack(t, clientSide1); ca.ReturnCode != codec.Accepted {
		t.Fatalf("first CONNACK ReturnCode = 0x%02x, want Accepted", ca.ReturnCode)
	}
	waitRegistered(t, srv, "dev")
	// Also wait for the first established event to be observed before the
	// second connection exists, so the asserted order is deterministic.
	deadline := time.Now().Add(2 * time.Second)
	for len(rec.snapshot()) < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	if err := sendConnect(clientSide2, codec.MagicNumber, codec.ProtocolVersion, "dev"); err != nil {
		t.Fatalf("second CONNECT write error: %v", err)
	}
	if ca := readConnack(t, clientSide2); ca.ReturnCode != codec.Accepted {
		t.Fatalf("second CONNACK ReturnCode = 0x%02x, want Accepted", ca.ReturnCode)
	}

	// wait for the takeover event sequence to settle
	deadline = time.Now().Add(2 * time.Second)
	for len(rec.snapshot()) < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	events := rec.snapshot()
	for _, e := range events {
		t.Logf("listener event: %s", e)
	}

	want := []string{
		"established(dev)",
		"closed(dev, \"session taken over\")",
		"established(dev)",
	}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}

	// drain the takeover DISCONNECT and let both handlers finish
	cp, err := codec.ReadPacket(clientSide1)
	if err != nil {
		t.Fatalf("first connection DISCONNECT read error: %v", err)
	}
	if dp, ok := cp.(*codec.DisconnectPacket); !ok || dp.GetReturnCode() != codec.DiscSessionTakenOver {
		t.Errorf("first connection got %v, want DISCONNECT with DiscSessionTakenOver", cp)
	}
	select {
	case <-done1:
	case <-time.After(2 * time.Second):
		t.Fatal("first handleConnection did not return after takeover")
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

	// the graceful close of the surviving connection must add exactly one
	// more closed event
	deadline = time.Now().Add(2 * time.Second)
	for len(rec.snapshot()) < 4 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	events = rec.snapshot()
	if len(events) != 4 {
		t.Fatalf("events after disconnect = %v, want 4 entries", events)
	}
	if !strings.HasPrefix(events[3], "closed(dev") {
		t.Errorf("last event = %q, want closed(dev, ...)", events[3])
	}
}
