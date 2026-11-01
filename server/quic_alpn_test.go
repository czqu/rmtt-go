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
		if ln.boundLn() != nil {
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
// the handshake failed with CRYPTO_ERROR 0x178 (no_application_protocol). With
// NextProtos unset, the listener now advertises the default codec.ALPN.
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

// TestQUICListener_HonoursExplicitALPNOverride ensures an explicit NextProtos is
// used verbatim (the rmtt default is NOT injected), so callers can select a
// different token.
func TestQUICListener_HonoursExplicitALPNOverride(t *testing.T) {
	serverCfg := mustGenerateTLSConfigWithALPN(t, []string{"h2"})

	ln := NewQUICListener(fmt.Sprintf("127.0.0.1:%d", freeUDPPort(t)), serverCfg).(*quicListener)
	serveQUICInBackground(t, ln, ln.addr)

	// A client offering the caller's chosen token "h2" must connect.
	dialCfg := &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}}
	sess := dialQUICRetry(t, ln.addr, dialCfg)
	defer sess.CloseWithError(0, "")

	if got := sess.ConnectionState().TLS.NegotiatedProtocol; got != "h2" {
		t.Fatalf("NegotiatedProtocol = %q, want %q", got, "h2")
	}
	if want := []string{"h2"}; len(serverCfg.NextProtos) != 1 || serverCfg.NextProtos[0] != want[0] {
		t.Errorf("caller's NextProtos changed to %v, want %v", serverCfg.NextProtos, want)
	}
}

// TestQUICListener_DisabledALPNAcceptsLegacyClient covers the deprecated
// DisableALPN sentinel: ALPN is turned off so legacy peers that send none can
// still connect. Disabling ALPN is non-compliant for QUIC (RFC 9001) and exists
// only for migration — see codec.DisableALPN.
func TestQUICListener_DisabledALPNAcceptsLegacyClient(t *testing.T) {
	serverCfg := mustGenerateTLSConfigWithALPN(t, []string{codec.DisableALPN})

	ln := NewQUICListener(fmt.Sprintf("127.0.0.1:%d", freeUDPPort(t)), serverCfg).(*quicListener)
	serveQUICInBackground(t, ln, ln.addr)

	// Legacy-style client: offers no ALPN at all.
	dialCfg := &tls.Config{InsecureSkipVerify: true}
	sess := dialQUICRetry(t, ln.addr, dialCfg)
	defer sess.CloseWithError(0, "")

	if got := sess.ConnectionState().TLS.NegotiatedProtocol; got != "" {
		t.Fatalf("NegotiatedProtocol = %q, want empty (ALPN disabled)", got)
	}
}

// TestQUIC_GoClientWithGoServer_OmittedALPN guards the symmetric regression:
// with both ends leaving NextProtos unset, both take the default "rmtt" and the
// handshake succeeds (a naive Go client that offered nothing would be rejected
// by the ALPN-requiring server; the client path supplies the default too).
func TestQUIC_GoClientWithGoServer_OmittedALPN(t *testing.T) {
	serverCfg := mustGenerateTLSConfigNoALPN(t)
	ln := NewQUICListener(fmt.Sprintf("127.0.0.1:%d", freeUDPPort(t)), serverCfg).(*quicListener)
	serveQUICInBackground(t, ln, ln.addr)

	// A Go-style client tls.Config with no NextProtos; the client transport
	// layers codec.WithDefaultALPN on top before dialing.
	rawClientCfg := &tls.Config{InsecureSkipVerify: true}
	sess := dialQUICRetry(t, ln.addr, codec.WithDefaultALPN(rawClientCfg))
	defer sess.CloseWithError(0, "")

	if got := sess.ConnectionState().TLS.NegotiatedProtocol; got != codec.ALPN {
		t.Fatalf("NegotiatedProtocol = %q, want %q", got, codec.ALPN)
	}
	if rawClientCfg.NextProtos != nil {
		t.Errorf("WithDefaultALPN mutated the client config: NextProtos = %v", rawClientCfg.NextProtos)
	}
}
