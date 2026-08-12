// Package integration_test exercises the rmtt-go server and client against
// each other end-to-end over a real TCP connection: connect, push in both
// directions, and keepalive (a heartbeating client stays alive while a silent
// one is reaped by the server).
package integration_test

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/czqu/rmtt-go/client"
	"github.com/czqu/rmtt-go/server"
)

// freePort reserves a listening port and returns it after closing the socket,
// so the server under test can bind the same port.
func freePort(t testing.TB) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// harness bundles a running server plus the state it collected during the test.
type harness struct {
	addr string
	srv  server.Server
	stop func()

	mu          sync.Mutex
	uplinkMsgs  []string
	established int
	closed      int
	connLost    chan struct{}
}

// newHarness starts a real rmtt-go server on a random port with the given
// keepalive policy. Devices are authenticated by their credential.
func newHarness(t testing.TB, policy *server.KeepalivePolicy) *harness {
	t.Helper()
	h := &harness{connLost: make(chan struct{}, 1)}
	port := freePort(t)

	h.srv = server.NewServer(
		server.NewServerOptions().
			SetPort(port).
			SetKeepalivePolicy(policy).
			SetAuthenticator(allowAuthenticator{}).
			SetMessageHandler(server.MessageHandler(func(deviceID string, payload []byte) {
				h.mu.Lock()
				h.uplinkMsgs = append(h.uplinkMsgs, fmt.Sprintf("%s:%s", deviceID, payload))
				h.mu.Unlock()
			})).
			SetConnectionListener(testListener{
				established: func(string) { h.mu.Lock(); h.established++; h.mu.Unlock() },
				closed:      func(string, string) { h.mu.Lock(); h.closed++; h.mu.Unlock() },
			}),
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = h.srv.ListenAndServeContext(ctx)
	}()
	h.stop = func() {
		cancel()
		_ = h.srv.Close()
		<-done
	}

	h.addr = fmt.Sprintf("tcp://127.0.0.1:%d", port)
	// Wait until the server has actually bound its listener, so a client
	// connecting right after newHarness never races the async Serve goroutine.
	waitFor(t, func() bool {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return true
	}, "server listener ready")
	return h
}

func (h *harness) activeConns() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.established - h.closed
}

// uplinks returns a snapshot of the uplink messages received so far.
func (h *harness) uplinks() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.uplinkMsgs...)
}

// allowAuthenticator accepts every credential and uses it as the device ID.
type allowAuthenticator struct{}

func (allowAuthenticator) Authenticate(credential string) (string, bool) {
	return credential, true
}

// testListener adapts server.ConnectionListener for the tests.
type testListener struct {
	established func(string)
	closed      func(string, string)
}

func (l testListener) OnConnectionEstablished(deviceID string) {
	if l.established != nil {
		l.established(deviceID)
	}
}
func (l testListener) OnConnectionClosed(deviceID string, reason string) {
	if l.closed != nil {
		l.closed(deviceID, reason)
	}
}

// connectClient dials addr with the given credential and waits for the
// CONNACK, returning a ready Client.
func connectClient(t testing.TB, addr, cred string, heartbeat time.Duration) client.Client {
	t.Helper()
	o := client.NewClientOptions()
	o.Servers = nil
	o.AddServer(addr)
	o.SetCredential(cred)
	o.SetHeartbeat(heartbeat)
	o.SetConnectTimeout(3 * time.Second)
	o.SetWriteTimeout(3 * time.Second)
	o.AutoReconnect = false
	o.ConnectRetry = false
	c := client.NewClient(o)

	tok := c.Connect()
	if !tok.WaitTimeout(5 * time.Second) {
		t.Fatal("Connect() token did not complete")
	}
	if tok.Error() != nil {
		t.Fatalf("Connect() error: %v", tok.Error())
	}
	if !c.IsConnected() {
		t.Fatal("client not connected after CONNACK accepted")
	}
	return c
}

// waitFor polls cond until it holds or the timeout elapses.
func waitFor(t testing.TB, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestIntegration_ConnectAndBidirectionalPush verifies the full round trip:
// client connects, uplink push reaches the server handler, and a server-side
// push is delivered to the client's payload handler.
func TestIntegration_ConnectAndBidirectionalPush(t *testing.T) {
	h := newHarness(t, server.DefaultKeepalivePolicy())
	defer h.stop()

	c := connectClient(t, h.addr, "device-a", 10*time.Second)
	defer c.Disconnect(100)

	got := make(chan string, 4)
	c.AddPayloadHandlerLast(func(cl client.Client, msg client.Message) {
		got <- string(msg.Payload())
	})

	// Uplink: client -> server.
	ptok := c.Push("hello server")
	if !ptok.WaitTimeout(3 * time.Second) {
		t.Fatal("Push() token did not complete")
	}
	if ptok.Error() != nil {
		t.Fatalf("Push() error: %v", ptok.Error())
	}
	waitFor(t, func() bool { return len(h.uplinks()) == 1 }, "uplink delivered to server handler")
	if got := h.uplinks()[0]; got != "device-a:hello server" {
		t.Fatalf("server handler got %q", got)
	}

	// Downlink: server -> client, routed through the real ConnectionStore.
	if err := h.srv.Push("device-a", []byte("hello device-a")); err != nil {
		t.Fatalf("server Push() error: %v", err)
	}
	select {
	case m := <-got:
		if m != "hello device-a" {
			t.Fatalf("client received %q", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client did not receive server push")
	}
}

// TestIntegration_HeartbeatKeepsConnectionAlive verifies that a client sending
// heartbeats stays registered on the server (not reaped by keepalive timeout)
// and can still push after several heartbeat intervals.
func TestIntegration_HeartbeatKeepsConnectionAlive(t *testing.T) {
	// 1s keepalive so the test completes quickly; the client also heartbeats
	// every 1s, so the server reaps nothing.
	policy := &server.KeepalivePolicy{MinSeconds: 1, MaxSeconds: 5, DefaultSeconds: 1}
	h := newHarness(t, policy)
	defer h.stop()

	c := connectClient(t, h.addr, "device-hb", time.Second)
	defer c.Disconnect(100)

	// Hold the connection across several heartbeat intervals; the server must
	// not reap it.
	time.Sleep(2500 * time.Millisecond)
	if got := h.activeConns(); got != 1 {
		t.Fatalf("server active connections = %d, want 1 (client should have been kept alive)", got)
	}
	if !c.IsConnected() {
		t.Fatal("client reported disconnected while heartbeating")
	}

	// A push still works end-to-end after the heartbeat traffic.
	ptok := c.Push("still alive")
	if !ptok.WaitTimeout(3*time.Second) || ptok.Error() != nil {
		t.Fatalf("push after heartbeats failed: %v", ptok.Error())
	}
	waitFor(t, func() bool { return len(h.uplinks()) == 1 }, "post-heartbeat uplink delivered")
}

// TestIntegration_SilentClientIsReaped verifies the server's keepalive timeout:
// a client that never sends heartbeats is disconnected once the negotiated
// keepalive window lapses.
func TestIntegration_SilentClientIsReaped(t *testing.T) {
	// Client proposes 0 (never heartbeats); AllowDisable=false makes the server
	// fall back to DefaultSeconds=1, so the reap timeout fires ~1.5s.
	policy := &server.KeepalivePolicy{MinSeconds: 1, MaxSeconds: 5, DefaultSeconds: 1, AllowDisable: false}
	h := newHarness(t, policy)
	defer h.stop()

	c := connectClient(t, h.addr, "device-silent", 0)
	defer c.Disconnect(100)

	// Wait for the server to reap the silent connection.
	waitFor(t, func() bool { return h.activeConns() == 0 }, "server to reap silent client")
}
