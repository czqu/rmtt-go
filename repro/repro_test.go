// Package repro contains repro tests that confirm the code-review findings
// against the current code. Each test logs "REPRO CONFIRMED" when the buggy
// behaviour under review is observed. Run with:
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
	"fmt"
	"math/big"
	"net"
	"net/url"
	"runtime"
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
	cm.MagicNumber = 0x637a7175
	cm.ProtocolVersion = 1
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

// ---------- C1: codec RemainingLength unbounded allocation ----------

// TestRepro_C1_RemainingLengthAllocDOS shows that a 5-byte packet header makes
// codec.ReadPacket allocate the claimed RemainingLength (here 128MiB) before a
// single payload byte arrives, with no upper bound and before authentication.
func TestRepro_C1_RemainingLengthAllocDOS(t *testing.T) {
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
	t.Logf("attacker sent %d bytes, ReadPacket err=%v, heap allocated for claimed length: %d bytes (%.1f MiB)",
		len(pkt), err, delta, float64(delta)/(1<<20))

	if delta >= claimed-1<<20 {
		t.Logf("REPRO CONFIRMED: ReadPacket allocated ~claimed size (%d bytes) from a %d-byte header with no size cap (pre-auth DoS)",
			claimed, len(pkt))
	} else {
		t.Logf("NOT REPRODUCED: allocation delta = %d", delta)
	}
}

// ---------- C2: CONNACK refusal 0x02 treated as connection success ----------

// TestRepro_C2_RefusedConnackTreatedAsSuccess shows that a server answering
// CONNACK(0x02 = ErrRefusedServerUnavailable) yields a Connect token with
// Error()==nil and IsConnected()==true, and the token even reports the
// Accepted return code.
func TestRepro_C2_RefusedConnackTreatedAsSuccess(t *testing.T) {
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
		srvConnCh <- conn // keep the socket open so no EOF races the assertions
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

	t.Logf("Connect: WaitTimeout=%v, Error()=%v, IsConnected()=%v, ReturnCode()=0x%02x (server actually sent 0x%02x)",
		done, tok.Error(), c.IsConnected(), tok.(*client.ConnectToken).ReturnCode(), codec.ErrRefusedServerUnavailable)

	if done && tok.Error() == nil && c.IsConnected() {
		t.Logf("REPRO CONFIRMED: server refused with CONNACK 0x02 but client reports connected, token Error()==nil, ReturnCode()=Accepted")
	} else {
		t.Logf("NOT REPRODUCED: done=%v err=%v connected=%v", done, tok.Error(), c.IsConnected())
	}
}

// ---------- W1: CONNECT handshake has no read deadline (slowloris) ----------

// TestRepro_W1_HandshakeNoDeadline shows the server holds a connection that
// never sends a byte: after 2.5s the client-side read returns a timeout (conn
// still open) instead of EOF, proving no handshake deadline exists.
func TestRepro_W1_HandshakeNoDeadline(t *testing.T) {
	opts := server.NewServerOptions()
	_, addr, stop := startRealServer(t, opts)
	defer stop()

	conn := dialRetry(t, addr)
	defer conn.Close()
	// send nothing at all

	time.Sleep(2500 * time.Millisecond)

	_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	_, err := conn.Read(make([]byte, 1))
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Logf("read err=%v (connection still held open by server)", err)
		t.Logf("REPRO CONFIRMED: zero-byte connection still accepted and pending after 2.5s — CONNECT parse has no deadline (slowloris holds goroutine+fd)")
	} else {
		t.Logf("NOT REPRODUCED: read err=%v", err)
	}
}

// ---------- W2: Kick double-fires OnConnectionClosed ----------

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

// TestRepro_W2_KickDoubleCloseEvent shows that server.Kick delivers TWO
// OnConnectionClosed callbacks for the same device.
func TestRepro_W2_KickDoubleCloseEvent(t *testing.T) {
	rec := &eventRecorder{}
	opts := server.NewServerOptions()
	opts.SetConnectionListener(rec)
	srv, addr, stop := startRealServer(t, opts)
	defer stop()

	conn := dialRetry(t, addr)
	defer conn.Close()
	ca := sendConnect(t, conn, "dev")
	if ca.ReturnCode != codec.Accepted {
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
	if closed >= 2 {
		t.Logf("REPRO CONFIRMED: Kick produced %d OnConnectionClosed callbacks for one device (duplicated lifecycle event)", closed)
	} else {
		t.Logf("NOT REPRODUCED: closed events = %d", closed)
	}
}

// ---------- W5: OnConnectAttempt's returned tls.Config is ignored ----------

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

// TestRepro_W5_OnConnectAttemptIgnored shows that the tls.Config returned by
// the OnConnectAttempt handler is never used: the handler deliberately returns
// a config with a wrong ServerName (handshake MUST fail if applied), yet the
// connection succeeds.
func TestRepro_W5_OnConnectAttemptIgnored(t *testing.T) {
	cert, pool := selfSignedCert(t)

	opts := server.NewServerOptions()
	addr := freeAddr(t)
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

	// wait for the TLS listener to be ready
	if c := dialRetry(t, addr); c != nil {
		c.Close()
	}

	handlerCalls := 0
	copts := client.NewClientOptions()
	copts.AddServer("tls://" + addr)
	copts.SetCredential("dev")
	copts.ConnectRetry = false
	copts.AutoReconnect = false
	copts.SetConnectTimeout(5 * time.Second)
	copts.SetTlsConfig(&tls.Config{RootCAs: pool, ServerName: "localhost", MinVersion: tls.VersionTLS12})
	copts.SetConnectionAttemptHandler(func(u *url.URL, cfg *tls.Config) *tls.Config {
		handlerCalls++
		// Deliberately broken config: wrong ServerName. Per the documented
		// contract ("may return a modified tls.Config for that attempt") the
		// handshake must fail with this config.
		return &tls.Config{RootCAs: pool, ServerName: "wrong.invalid", MinVersion: tls.VersionTLS12}
	})

	c := client.NewClient(copts)
	tok := c.Connect()
	done := tok.WaitTimeout(8 * time.Second)

	t.Logf("Connect: done=%v, Error()=%v, IsConnected()=%v, handlerCalls=%d", done, tok.Error(), c.IsConnected(), handlerCalls)
	if done && tok.Error() == nil && handlerCalls > 0 {
		t.Logf("REPRO CONFIRMED: handler returned a cert-mismatching tls.Config but connect succeeded — OnConnectAttempt's return value is ignored")
	} else {
		t.Logf("NOT REPRODUCED: done=%v err=%v handlerCalls=%d", done, tok.Error(), handlerCalls)
	}
	c.Disconnect(100)
}
