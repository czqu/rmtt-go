package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/czqu/rmtt-go/codec"
	"github.com/quic-go/quic-go"
)

// freeUDPPort reserves a UDP port and returns its number after closing the
// socket, so the QUIC listener under test can bind it. TCP and UDP port
// namespaces are independent, which is why this does not reuse freePort.
func freeUDPPort(t *testing.T) int {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	port := pc.LocalAddr().(*net.UDPAddr).Port
	_ = pc.Close()
	return port
}

// serveQUICInBackground starts ln.Serve on a concrete addr and waits until the
// underlying quic listener is bound so a client dial never races the bind.
func serveQUICInBackground(t *testing.T, ln *quicListener, addr string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = ln.Serve(ctx, func(net.Conn) {}) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ln.ln != nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("quic listener did not bind %s in time", addr)
}

// dialQUICRetry dials addr with cfg, retrying briefly so it does not race the
// asynchronous Serve goroutine's bind.
func dialQUICRetry(t *testing.T, addr string, cfg *tls.Config) *quic.Conn {
	t.Helper()
	var lastErr error
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		sess, err := quic.DialAddr(ctx, addr, cfg, defaultQuicConf())
		cancel()
		if err == nil {
			return sess
		}
		lastErr = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("quic dial %s: %v", addr, lastErr)
	return nil
}

// TestQUICListener_NegotiatesRMTTWithNettyClient is the regression test for the
// Java (Netty) -> Go incompatibility: the Netty QUIC client forces the server to
// negotiate ALPN "rmtt", but rmtt-go's QUIC listener left NextProtos empty, so
// the handshake failed with CRYPTO_ERROR 0x178 (no_application_protocol). The
// listener must now advertise codec.ALPN even when the caller's tls.Config
// omits NextProtos.
func TestQUICListener_NegotiatesRMTTWithNettyClient(t *testing.T) {
	// The typical user-supplied config: a certificate but no NextProtos.
	serverCfg := mustGenerateTLSConfigNoALPN(t)
	if len(serverCfg.NextProtos) != 0 {
		t.Fatalf("precondition: serverCfg.NextProtos = %v, want empty", serverCfg.NextProtos)
	}

	ln := NewQUICListener(fmt.Sprintf("127.0.0.1:%d", freeUDPPort(t)), serverCfg).(*quicListener)
	serveQUICInBackground(t, ln, ln.addr)

	// Netty-style client: offers exactly ALPN "rmtt" and expects it selected.
	dialCfg := &tls.Config{InsecureSkipVerify: true, NextProtos: []string{codec.ALPN}}
	sess := dialQUICRetry(t, ln.addr, dialCfg)
	defer sess.CloseWithError(0, "")

	if got := sess.ConnectionState().TLS.NegotiatedProtocol; got != codec.ALPN {
		t.Fatalf("NegotiatedProtocol = %q, want %q", got, codec.ALPN)
	}

	// The caller's config must not be mutated: it may be shared with, say, a
	// WSS listener that must NOT advertise "rmtt".
	if len(serverCfg.NextProtos) != 0 {
		t.Errorf("listener mutated caller's tls.Config: NextProtos = %v, want empty", serverCfg.NextProtos)
	}
}

// TestQUICListener_PreservesCallerALPN ensures an explicit NextProtos choice is
// respected: the rmtt token is added without discarding what the caller set.
func TestQUICListener_PreservesCallerALPN(t *testing.T) {
	serverCfg := mustGenerateTLSConfigWithALPN(t, []string{"h2"})

	ln := NewQUICListener(fmt.Sprintf("127.0.0.1:%d", freeUDPPort(t)), serverCfg).(*quicListener)
	serveQUICInBackground(t, ln, ln.addr)

	dialCfg := &tls.Config{InsecureSkipVerify: true, NextProtos: []string{codec.ALPN}}
	sess := dialQUICRetry(t, ln.addr, dialCfg)
	defer sess.CloseWithError(0, "")

	if got := sess.ConnectionState().TLS.NegotiatedProtocol; got != codec.ALPN {
		t.Fatalf("NegotiatedProtocol = %q, want %q", got, codec.ALPN)
	}
	if want := []string{"h2"}; len(serverCfg.NextProtos) != 1 || serverCfg.NextProtos[0] != want[0] {
		t.Errorf("caller's NextProtos changed to %v, want %v", serverCfg.NextProtos, want)
	}
}

// TestQUIC_GoClientWithGoServer_OmittedALPN guards the symmetric regression:
// once the server advertises "rmtt", a Go client that also omits NextProtos
// must still connect because the client path offers codec.ALPN via WithALPN.
// Without that, RFC 9001 ALPN negotiation would fail (server offers a protocol
// the client never requested).
func TestQUIC_GoClientWithGoServer_OmittedALPN(t *testing.T) {
	serverCfg := mustGenerateTLSConfigNoALPN(t)
	ln := NewQUICListener(fmt.Sprintf("127.0.0.1:%d", freeUDPPort(t)), serverCfg).(*quicListener)
	serveQUICInBackground(t, ln, ln.addr)

	// A Go-style client tls.Config with no NextProtos; the client transport
	// layers codec.WithALPN on top before dialing.
	rawClientCfg := &tls.Config{InsecureSkipVerify: true}
	sess := dialQUICRetry(t, ln.addr, codec.WithALPN(rawClientCfg))
	defer sess.CloseWithError(0, "")

	if got := sess.ConnectionState().TLS.NegotiatedProtocol; got != codec.ALPN {
		t.Fatalf("NegotiatedProtocol = %q, want %q", got, codec.ALPN)
	}
	if rawClientCfg.NextProtos != nil {
		t.Errorf("WithALPN mutated the client config: NextProtos = %v", rawClientCfg.NextProtos)
	}
}
