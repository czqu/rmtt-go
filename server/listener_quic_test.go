package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/czqu/rmtt-go/codec"
	"github.com/quic-go/quic-go"
)

// runtimeSelfSignedCert generates a fresh self-signed ed25519 certificate at
// runtime so the QUIC tests need no checked-in cert files and stay hermetic.
func runtimeSelfSignedCert() (tls.Certificate, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IsCA:         true,
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, pub, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, nil
}

func mustGenerateTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	cert, err := runtimeSelfSignedCert()
	if err != nil {
		t.Fatalf("runtime cert: %v", err)
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"rmtt"},
		MinVersion:   tls.VersionTLS13,
	}
}

// acceptStreamSoon waits for the server-side session to accept one stream and
// returns it as a quicStreamConn, failing the test after a timeout instead of
// hanging the whole test binary.
func acceptStreamSoon(t *testing.T, sess *quic.Conn) *quicStreamConn {
	t.Helper()
	type res struct {
		st  *quic.Stream
		err error
	}
	ch := make(chan res, 1)
	go func() {
		st, err := sess.AcceptStream(context.Background())
		ch <- res{st, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("server AcceptStream: %v", r.err)
		}
		return &quicStreamConn{Stream: r.st, session: sess}
	case <-time.After(3 * time.Second):
		t.Fatal("server AcceptStream timed out")
		return nil
	}
}

// TestQUICMultiStream_CloseDoesNotKillSession opens a QUIC session, starts two
// independent rmtt device streams over it, and verifies that closing one
// stream's net.Conn does NOT tear down the other — the regression fixed by
// making quicStreamConn.Close only close its stream (the session is owned by
// serveSession).
func TestQUICMultiStream_CloseDoesNotKillSession(t *testing.T) {
	tlsCfg := mustGenerateTLSConfig(t)

	ln, err := quic.ListenAddr("127.0.0.1:0", tlsCfg, defaultQuicConf())
	if err != nil {
		t.Fatalf("quic.ListenAddr: %v", err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Server side: accept the single QUIC session; serveSession-style stream
	// acceptance is driven per-stream by acceptStreamSoon below so the test
	// stays synchronous and fails fast instead of deadlocking.
	sessCh := make(chan *quic.Conn, 1)
	go func() {
		sess, err := ln.Accept(ctx)
		if err != nil {
			return
		}
		sessCh <- sess
	}()

	// Client side: dial one QUIC session.
	dialTLS := &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"rmtt"}}
	sess, err := quic.DialAddr(ctx, ln.Addr().String(), dialTLS, defaultQuicConf())
	if err != nil {
		t.Fatalf("quic.DialAddr: %v", err)
	}
	defer sess.CloseWithError(0, "")

	// Wait for the server to accept the session.
	var serverSess *quic.Conn
	select {
	case serverSess = <-sessCh:
	case <-time.After(3 * time.Second):
		t.Fatal("server ln.Accept timed out")
	}
	defer serverSess.CloseWithError(0, "")

	// Open two client-side streams; the server accepts each as a
	// quicStreamConn (one rmtt device connection per stream). A PINGREQ is
	// written right after each OpenStreamSync so quic-go delivers the STREAM
	// frame to the peer, making AcceptStream return; the server then drains
	// that PINGREQ so the stream is left clean for the later assertion.
	st1, err := sess.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("open stream1: %v", err)
	}
	if err := writePing(st1); err != nil {
		t.Fatalf("ping stream1: %v", err)
	}
	conn1 := acceptStreamSoon(t, serverSess)
	if _, err := codec.ReadPacket(conn1); err != nil {
		t.Fatalf("drain ping1: %v", err)
	}

	st2, err := sess.OpenStreamSync(ctx)
	if err != nil {
		t.Fatalf("open stream2: %v", err)
	}
	if err := writePing(st2); err != nil {
		t.Fatalf("ping stream2: %v", err)
	}
	conn2 := acceptStreamSoon(t, serverSess)
	if _, err := codec.ReadPacket(conn2); err != nil {
		t.Fatalf("drain ping2: %v", err)
	}
	defer conn2.Close()

	// Close the first stream only; previously this also closed the session,
	// killing conn2 mid-flight.
	if err := conn1.Close(); err != nil {
		t.Fatalf("conn1.Close: %v", err)
	}

	// conn2 must still be usable: client writes a PINGREQ, server reads it.
	if err := writePing(st2); err != nil {
		t.Fatalf("write ping on stream2 after conn1 close: %v", err)
	}
	if _, err := codec.ReadPacket(conn2); err != nil {
		t.Fatalf("ReadPacket on conn2 after conn1 close: %v (session was torn down by stream close)", err)
	}
}

// writePing writes a PINGREQ on w.
func writePing(w interface{ Write([]byte) (int, error) }) error {
	ping := codec.NewControlPacket(codec.Pingreq).(*codec.PingreqPacket)
	return ping.Write(w)
}
