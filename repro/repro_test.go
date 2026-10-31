// Package repro holds regression tests derived from the code review of this
// repository. Each test originally reproduced a defect and now asserts the
// fixed behaviour. Run with:
//
//	go test -v ./repro/
package repro

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/czqu/rmtt-go/client"
	"github.com/czqu/rmtt-go/codec"
	"github.com/czqu/rmtt-go/server"
)

// ---------- helpers ----------

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func dialRetry(t *testing.T, addr string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			return conn
		}
		if time.Now().After(deadline) {
			t.Fatalf("dial %s: %v", addr, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// startRealServer starts a real rmtt server on a free TCP port.
func startRealServer(t *testing.T, opts *server.ServerOptions) (server.Server, string, func()) {
	t.Helper()
	addr := freeAddr(t)
	opts.AddListener(server.NewTCPListener(addr))
	srv := server.NewServer(opts)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.ListenAndServeContext(ctx) }()
	return srv, addr, func() {
		cancel()
		_ = srv.Close()
	}
}

func sendConnect(t *testing.T, conn net.Conn, credential string) *codec.ConnackPacket {
	t.Helper()
	cm := codec.NewControlPacket(codec.Connect).(*codec.ConnectPacket)
	cm.MagicNumber = codec.MagicNumber
	cm.ProtocolVersion = codec.ProtocolVersion
	cm.Keepalive = 30
	cm.Credential = credential
	if err := cm.Write(conn); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	cp, err := codec.ReadPacket(conn)
	if err != nil {
		t.Fatalf("read CONNACK: %v", err)
	}
	return cp.(*codec.ConnackPacket)
}

// ---------- C1: codec RemainingLength bounded ----------

// TestC1_RemainingLengthCapped asserts that a header claiming a huge
// RemainingLength is rejected before any allocation: a 5-byte packet must not
// make ReadPacket allocate the claimed size (previously 128MiB from 5 bytes,
// pre-authentication).
func TestC1_RemainingLengthCapped(t *testing.T) {
	// varint 80 80 80 40 decodes to 64<<21 = 134217728 (128MiB).
	const claimed = 134217728
	// type byte: Push (3<<4), flags 0; then the 4-byte varint above.
	pkt := []byte{0x30, 0x80, 0x80, 0x80, 0x40}

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	_, err := codec.ReadPacket(bytes.NewReader(pkt))

	runtime.ReadMemStats(&after)
	delta := after.TotalAlloc - before.TotalAlloc

	if !errors.Is(err, codec.ErrPacketTooLarge) {
		t.Fatalf("ReadPacket() error = %v, want ErrPacketTooLarge", err)
	}
	if delta >= 1<<20 {
		t.Fatalf("ReadPacket allocated %d bytes for a rejected %d-byte claim", delta, claimed)
	}
	t.Logf("rejected %d-byte claim with %d bytes allocated (err=%v)", claimed, delta, err)

	// The varint itself must also reject a fifth length byte instead of
	// silently truncating.
	if _, err := codec.ReadPacket(bytes.NewReader([]byte{0x30, 0xFF, 0xFF, 0xFF, 0xFF, 0x7F})); !errors.Is(err, codec.ErrMalformedRemainingLength) {
		t.Fatalf("5-byte varint error = %v, want ErrMalformedRemainingLength", err)
	}
}

// ---------- C2: refused CONNACK must fail the connect ----------

// TestC2_RefusedConnackFailsConnect asserts that a server answering
// CONNACK(0x02 = ErrRefusedServerUnavailable) fails the Connect token with
// that code surfaced — previously the client reported connected, Error()==nil
// and ReturnCode()==Accepted.
func TestC2_RefusedConnackFailsConnect(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	srvConnCh := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		if _, err := codec.ReadPacket(conn); err != nil {
			conn.Close()
			return
		}
		ca := codec.NewControlPacket(codec.Connack).(*codec.ConnackPacket)
		ca.ReturnCode = codec.ErrRefusedServerUnavailable // 0x02: server said NO
		ca.ServerKeepalive = 60
		_ = ca.Write(conn)
		srvConnCh <- conn // hold the socket so no EOF races the assertions
	}()

	opts := client.NewClientOptions()
	opts.AddServer("tcp://" + ln.Addr().String())
	opts.SetCredential("dev")
	opts.ConnectRetry = false
	opts.AutoReconnect = false
	opts.SetConnectTimeout(3 * time.Second)

	c := client.NewClient(opts)
	tok := c.Connect()
	done := tok.WaitTimeout(5 * time.Second)
	srvConn := <-srvConnCh
	defer srvConn.Close()

	if !done {
		t.Fatal("Connect() token did not complete")
	}
	if !errors.Is(tok.Error(), client.RefusedServerUnavailableErr) {
		t.Fatalf("Connect() error = %v, want RefusedServerUnavailableErr", tok.Error())
	}
	if c.IsConnected() {
		t.Error("IsConnected() = true after server refusal, want false")
	}
	if rc := tok.(*client.ConnectToken).ReturnCode(); rc != codec.ErrRefusedServerUnavailable {
		t.Errorf("ReturnCode() = 0x%02x, want 0x%02x", rc, codec.ErrRefusedServerUnavailable)
	}
}

// ---------- W1: idle handshake must be closed by deadline ----------

// TestW1_HandshakeTimeoutClosesIdleConn asserts that a connection which sends
// nothing is closed by the server within the handshake timeout — previously
// the server held such connections forever (slowloris).
func TestW1_HandshakeTimeoutClosesIdleConn(t *testing.T) {
	opts := server.NewServerOptions()
	opts.SetHandshakeTimeout(500 * time.Millisecond)
	_, addr, stop := startRealServer(t, opts)
	defer stop()

	conn := dialRetry(t, addr)
	defer conn.Close()
	// send nothing at all

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err := conn.Read(make([]byte, 1))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("read err = %v, want EOF (server should close idle handshake after 500ms)", err)
	}
}

// ---------- W2: lifecycle events fire exactly once ----------

type eventRecorder struct {
	mu     sync.Mutex
	events []string
}

func (r *eventRecorder) OnConnectionEstablished(deviceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, fmt.Sprintf("established(%s)", deviceID))
}

func (r *eventRecorder) OnConnectionClosed(deviceID string, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, fmt.Sprintf("closed(%s, %q)", deviceID, reason))
}

func (r *eventRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...)
}

// TestW2_KickFiresSingleCloseEvent asserts that server.Kick delivers exactly
// ONE OnConnectionClosed callback — previously it double-fired (once from
// Kick, once from the read-loop teardown).
func TestW2_KickFiresSingleCloseEvent(t *testing.T) {
	rec := &eventRecorder{}
	opts := server.NewServerOptions()
	opts.SetConnectionListener(rec)
	srv, addr, stop := startRealServer(t, opts)
	defer stop()

	conn := dialRetry(t, addr)
	defer conn.Close()
	if ca := sendConnect(t, conn, "dev"); ca.ReturnCode != codec.Accepted {
		t.Fatalf("CONNACK = 0x%02x", ca.ReturnCode)
	}

	// wait until the server registered the device
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := srv.Push("dev", []byte("warmup")); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := srv.Kick("dev", codec.DiscKickedByAdmin); err != nil {
		t.Fatalf("Kick: %v", err)
	}
	time.Sleep(1 * time.Second)

	ev := rec.snapshot()
	for _, e := range ev {
		t.Logf("listener event: %s", e)
	}
	closed := 0
	for _, e := range ev {
		if len(e) >= 6 && e[:6] == "closed" {
			closed++
		}
	}
	if closed != 1 {
		t.Fatalf("Kick produced %d OnConnectionClosed callbacks, want exactly 1", closed)
	}
	for _, e := range ev {
		if len(e) >= 6 && e[:6] == "closed" && !strings.Contains(e, "kicked") {
			t.Errorf("closed event reason = %q, want kicked reason", e)
		}
	}
}

// ---------- W5: OnConnectAttempt's returned tls.Config is applied ----------

func selfSignedCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "localhost"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv, Leaf: leaf}, pool
}

// TestW5_OnConnectAttemptConfigApplied asserts that the tls.Config returned by
// the OnConnectAttempt handler is what the attempt actually uses — previously
// the handler ran after dialing and its return value was discarded.
func TestW5_OnConnectAttemptConfigApplied(t *testing.T) {
	cert, pool := selfSignedCert(t)

	addr := freeAddr(t)
	opts := server.NewServerOptions()
	opts.AddListener(server.NewTLSListener(addr, &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}))
	srv := server.NewServer(opts)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { _ = srv.ListenAndServeContext(ctx) }()
	defer func() {
		cancel()
		_ = srv.Close()
	}()
	if c := dialRetry(t, addr); c != nil {
		c.Close()
	}

	goodCfg := func() *tls.Config {
		return &tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12}
	}
	brokenCfg := func() *tls.Config {
		// wrong ServerName: handshake MUST fail if this config is applied
		return &tls.Config{RootCAs: pool, ServerName: "wrong.invalid", MinVersion: tls.VersionTLS12}
	}

	cases := []struct {
		name      string
		baseCfg   *tls.Config
		handler   *tls.Config
		wantError bool
	}{
		{"returned config is used (broken return must fail)", goodCfg(), brokenCfg(), true},
		{"returned config overrides options (good return must succeed)", brokenCfg(), goodCfg(), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handlerCalls := 0
			copts := client.NewClientOptions()
			copts.AddServer("tls://" + addr)
			copts.SetCredential("dev")
			copts.ConnectRetry = false
			copts.AutoReconnect = false
			copts.SetConnectTimeout(5 * time.Second)
			copts.SetTlsConfig(tc.baseCfg)
			copts.SetConnectionAttemptHandler(func(u *url.URL, cfg *tls.Config) *tls.Config {
				handlerCalls++
				return tc.handler
			})

			c := client.NewClient(copts)
			tok := c.Connect()
			done := tok.WaitTimeout(8 * time.Second)
			if !done {
				t.Fatal("Connect() token did not complete")
			}
			if handlerCalls != 1 {
				t.Errorf("handler calls = %d, want 1", handlerCalls)
			}
			if tc.wantError && tok.Error() == nil {
				t.Error("Connect() succeeded although the handler returned a cert-mismatching config; its return value is not applied")
			}
			if !tc.wantError && tok.Error() != nil {
				t.Errorf("Connect() error = %v, want success with the handler-returned config", tok.Error())
			}
			c.Disconnect(100)
		})
	}
}
