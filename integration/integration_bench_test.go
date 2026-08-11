// End-to-end benchmarks over a real TCP connection between the library's own
// client and server. Run with:
//
//	go test -bench=Benchmark -benchmem ./integration/
package integration_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"math/big"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/czqu/rmtt-go/client"
	"github.com/czqu/rmtt-go/server"
)

// benchEnv holds a running server and a connected client for benchmarking.
type benchEnv struct {
	h *harness
	c client.Client
}

// newBenchEnv starts a server and connects a client, ready for throughput
// benchmarks.
func newBenchEnv(b *testing.B) *benchEnv {
	b.Helper()
	h := newHarness(b, server.DefaultKeepalivePolicy())
	b.Cleanup(h.stop)

	c := connectClient(b, h.addr, "bench-dev", 10*time.Second)
	b.Cleanup(func() { c.Disconnect(100) })
	return &benchEnv{h: h, c: c}
}

// BenchmarkUplinkPush measures client -> server push throughput over a real
// TCP socket.
func BenchmarkUplinkPush(b *testing.B) {
	for _, size := range []int{64, 1024, 4096} {
		b.Run("payload="+fmt.Sprint(size), func(b *testing.B) {
			env := newBenchEnv(b)
			payload := make([]byte, size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tok := env.c.Push(payload)
				if !tok.WaitTimeout(5 * time.Second) {
					b.Fatal("push timed out")
				}
			}
		})
	}
}

// BenchmarkDownlinkPush measures server -> client push throughput, routed
// through the real ConnectionStore and read by the client's payload handler.
func BenchmarkDownlinkPush(b *testing.B) {
	for _, size := range []int{64, 1024, 4096} {
		b.Run("payload="+fmt.Sprint(size), func(b *testing.B) {
			env := newBenchEnv(b)
			payload := make([]byte, size)

			// Count downlink messages the client actually receives, ensuring
			// we measure the full path, not just the socket write.
			var received atomic.Int64
			env.c.AddPayloadHandlerLast(func(cl client.Client, msg client.Message) {
				received.Add(1)
			})

			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := env.h.srv.Push("bench-dev", payload); err != nil {
					b.Fatal(err)
				}
			}
			// Wait for the client to drain all N messages.
			b.StopTimer()
			deadline := time.Now().Add(5 * time.Second)
			for received.Load() < int64(b.N) && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			b.StartTimer()
			if got := received.Load(); got != int64(b.N) {
				b.Fatalf("client received %d/%d messages", got, b.N)
			}
		})
	}
}

// BenchmarkUplinkPushParallel measures concurrent client push throughput across
// multiple goroutines sharing one connection.
func BenchmarkUplinkPushParallel(b *testing.B) {
	for _, size := range []int{64, 1024} {
		b.Run("payload="+fmt.Sprint(size), func(b *testing.B) {
			env := newBenchEnv(b)
			payload := make([]byte, size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					tok := env.c.Push(payload)
					if !tok.WaitTimeout(5 * time.Second) {
						b.Fatal("push timed out")
					}
				}
			})
		})
	}
}

// BenchmarkBidirectionalPush drives uplink (client -> server) and downlink
// (server -> client) concurrently over the same connection, so uplink writes
// contend with downlink reads on the socket the way a real bidirectional
// telemetry session does. Each direction performs b.N/2 pushes (>=1), and the
// server routes every downlink push through the real ConnectionStore.
func BenchmarkBidirectionalPush(b *testing.B) {
	for _, size := range []int{64, 1024, 4096} {
		b.Run("payload="+fmt.Sprint(size), func(b *testing.B) {
			env := newBenchEnv(b)
			payload := make([]byte, size)

			// Count downlink messages the client actually drains, so we measure
			// the full path and not just the server-side socket write.
			var received atomic.Int64
			env.c.AddPayloadHandlerLast(func(cl client.Client, msg client.Message) {
				received.Add(1)
			})

			half := b.N / 2
			if half < 1 {
				half = 1
			}

			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()

			var wg sync.WaitGroup
			wg.Add(2)
			// Errors are collected on a channel instead of calling b.Fatal inside
			// the goroutines: go vet's testinggoroutine check rejects that, and
			// Fatal only Goexits the calling goroutine, which could deadlock the
			// WaitGroup.
			errCh := make(chan error, 2)
			go func() { // uplink producer
				defer wg.Done()
				for i := 0; i < half; i++ {
					tok := env.c.Push(payload)
					if !tok.WaitTimeout(5 * time.Second) {
						errCh <- errors.New("uplink push timed out")
						return
					}
				}
				errCh <- nil
			}()
			go func() { // downlink producer
				defer wg.Done()
				for i := 0; i < half; i++ {
					if err := env.h.srv.Push("bench-dev", payload); err != nil {
						errCh <- err
						return
					}
				}
				errCh <- nil
			}()
			wg.Wait()
			for i := 0; i < 2; i++ {
				if err := <-errCh; err != nil {
					b.Fatalf("bidirectional push failed: %v", err)
				}
			}

			// Ensure the client drained every downlink message.
			b.StopTimer()
			deadline := time.Now().Add(5 * time.Second)
			for received.Load() < int64(half) && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			b.StartTimer()
			if got := received.Load(); got != int64(half) {
				b.Fatalf("client received %d/%d downlink messages", got, half)
			}
		})
	}
}

// runtimeSelfSignedCertBench generates a fresh self-signed ed25519 certificate
// at runtime so the TLS-based transport benchmarks (tls://, quic://, wss://)
// need no checked-in cert files and stay hermetic.
func runtimeSelfSignedCertBench() (tls.Certificate, error) {
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

// transportEnv is a running server + connected client for one transport.
type transportEnv struct {
	srv  server.Server
	addr string
	c    client.Client
	stop func()
}

// newTransportEnv starts a server listening on the given transport scheme over
// a real socket and connects a client to it. TLS-based transports get a runtime
// self-signed certificate; the client skips verification. Clients retry the
// handshake until the server listener is ready, so non-TCP transports (QUIC,
// KCP, WS, WSS) that can't be probed with a plain TCP dial don't race.
func newTransportEnv(b *testing.B, scheme string) *transportEnv {
	b.Helper()
	port := freePort(b)

	var serverTLS, clientTLS *tls.Config
	switch scheme {
	case "tls", "quic":
		cert, err := runtimeSelfSignedCertBench()
		if err != nil {
			b.Fatalf("runtime cert: %v", err)
		}
		serverTLS = &tls.Config{
			Certificates: []tls.Certificate{cert},
			NextProtos:   []string{"rmtt"},
			MinVersion:   tls.VersionTLS13,
		}
		clientTLS = &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"rmtt"}}
	case "wss":
		// WSS serves an HTTP/1.1 websocket upgrade over TLS, not the RMTT
		// framing protocol, so its ALPN must NOT advertise "rmtt" — doing so
		// makes the server's TLS listener reject the handshake (unexpected EOF).
		cert, err := runtimeSelfSignedCertBench()
		if err != nil {
			b.Fatalf("runtime cert: %v", err)
		}
		serverTLS = &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}
		clientTLS = &tls.Config{InsecureSkipVerify: true}
	}

	opts := server.NewServerOptions().
		SetKeepalivePolicy(server.DefaultKeepalivePolicy()).
		SetAuthenticator(allowAuthenticator{}).
		SetConnectionListener(testListener{})
	addr := fmt.Sprintf(":%d", port)
	switch scheme {
	case "tcp":
		opts.AddListener(server.NewTCPListener(addr))
	case "kcp":
		opts.AddListener(server.NewKCPListener(addr))
	case "tls":
		opts.AddListener(server.NewTLSListener(addr, serverTLS))
	case "quic":
		opts.AddListener(server.NewQUICListener(addr, serverTLS))
	case "ws":
		opts.AddListener(server.NewWSListener(addr, ""))
	case "wss":
		opts.AddListener(server.NewWSSListener(addr, "", serverTLS))
	default:
		b.Fatalf("unknown transport scheme %q", scheme)
	}
	srv := server.NewServer(opts)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.ListenAndServeContext(ctx)
	}()
	stop := func() {
		cancel()
		_ = srv.Close()
		<-done
	}
	b.Cleanup(stop)

	// ws/client uses the default "/rmtt" path on both ends.
	caddr := fmt.Sprintf("%s://127.0.0.1:%d", scheme, port)
	c := connectClientRetry(b, caddr, "bench-dev", 10*time.Second, clientTLS)
	b.Cleanup(func() { c.Disconnect(100) })

	return &transportEnv{srv: srv, addr: caddr, c: c, stop: stop}
}

// connectClientRetry dials addr and waits for CONNACK, retrying until the
// server listener is ready (up to the deadline) so transports that can't be
// probed with a plain TCP dial don't race the async server goroutine.
func connectClientRetry(b *testing.B, addr, cred string, heartbeat time.Duration, tlsCfg *tls.Config) client.Client {
	b.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		o := client.NewClientOptions()
		o.Servers = nil
		o.AddServer(addr)
		o.SetCredential(cred)
		o.SetHeartbeat(heartbeat)
		o.SetConnectTimeout(2 * time.Second)
		o.SetWriteTimeout(2 * time.Second)
		o.AutoReconnect = false
		o.ConnectRetry = false
		if tlsCfg != nil {
			o.SetTlsConfig(tlsCfg)
		}
		c := client.NewClient(o)
		tok := c.Connect()
		if tok.WaitTimeout(3*time.Second) && tok.Error() == nil && c.IsConnected() {
			return c
		}
		c.Disconnect(100)
		if time.Now().After(deadline) {
			b.Fatalf("connect to %s failed: %v", addr, tok.Error())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// waitServerRegistered polls the server until the device is in its store, so
// downlink / bidirectional sub-benchmarks don't race the CONNACK-then-register
// ordering and fail with "device not connected" on the first push. The server
// writes CONNACK before registering the connection, so a connected client is
// not sufficient.
//
// The probe is a real empty push, which the client's payload handler counts as
// a received message. delivery is asynchronous, so this also waits for that
// warmup message to arrive and then resets received to 0 — otherwise the
// warmup would bleed into the drain check below.
func (env *transportEnv) waitServerRegistered(b *testing.B, deviceID string, received *atomic.Int64) {
	b.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if err := env.srv.Push(deviceID, nil); err == nil {
			break
		}
		if time.Now().After(deadline) {
			b.Fatalf("device %s did not register within 5s", deviceID)
		}
		time.Sleep(time.Millisecond)
	}
	for received.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	received.Store(0)
}

// transportBench runs one end-to-end push benchmark over the given transport
// scheme (tcp, kcp, tls, quic, ws, wss) in a given direction: "uplink" drives
// client -> server, "downlink" drives server -> client through the real
// ConnectionStore, and "bidirectional" drives both concurrently over the same
// connection.
func transportBench(b *testing.B, scheme, direction string, size int) {
	b.Helper()
	env := newTransportEnv(b, scheme)
	payload := make([]byte, size)

	// Count messages the client actually drains, so downlink / bidirectional
	// measure the full path, not just the server-side socket write.
	var received atomic.Int64
	env.c.AddPayloadHandlerLast(func(cl client.Client, msg client.Message) {
		received.Add(1)
	})

	b.SetBytes(int64(size))
	b.ReportAllocs()
	b.ResetTimer()

	switch direction {
	case "uplink":
		for i := 0; i < b.N; i++ {
			tok := env.c.Push(payload)
			if !tok.WaitTimeout(5 * time.Second) {
				b.Fatal("push timed out")
			}
		}
	case "downlink":
		env.waitServerRegistered(b, "bench-dev", &received)
		for i := 0; i < b.N; i++ {
			if err := env.srv.Push("bench-dev", payload); err != nil {
				b.Fatal(err)
			}
		}
		// Wait for the client to drain all N messages.
		b.StopTimer()
		deadline := time.Now().Add(5 * time.Second)
		for received.Load() < int64(b.N) && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		b.StartTimer()
		if got := received.Load(); got != int64(b.N) {
			b.Fatalf("client received %d/%d messages", got, b.N)
		}
	case "bidirectional":
		env.waitServerRegistered(b, "bench-dev", &received)
		half := b.N / 2
		if half < 1 {
			half = 1
		}
		var wg sync.WaitGroup
		wg.Add(2)
		// Errors are collected on a channel instead of calling b.Fatal inside
		// the goroutines: go vet's testinggoroutine check rejects that, and
		// Fatal only Goexits the calling goroutine, deadlocking the WaitGroup.
		errCh := make(chan error, 2)
		go func() { // uplink producer
			defer wg.Done()
			for i := 0; i < half; i++ {
				tok := env.c.Push(payload)
				if !tok.WaitTimeout(5 * time.Second) {
					errCh <- errors.New("uplink push timed out")
					return
				}
			}
			errCh <- nil
		}()
		go func() { // downlink producer
			defer wg.Done()
			for i := 0; i < half; i++ {
				if err := env.srv.Push("bench-dev", payload); err != nil {
					errCh <- err
					return
				}
			}
			errCh <- nil
		}()
		wg.Wait()
		for i := 0; i < 2; i++ {
			if err := <-errCh; err != nil {
				b.Fatalf("bidirectional push failed: %v", err)
			}
		}
		// Ensure the client drained every downlink message.
		b.StopTimer()
		deadline := time.Now().Add(5 * time.Second)
		for received.Load() < int64(half) && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		b.StartTimer()
		if got := received.Load(); got != int64(half) {
			b.Fatalf("client received %d/%d downlink messages", got, half)
		}
	default:
		b.Fatalf("unknown direction %q", direction)
	}
}

// BenchmarkTransportPush measures end-to-end push throughput over every
// transport the library supports, isolating each transport's raw ceiling. For
// each of tcp, kcp, tls, quic, ws and wss it runs the uplink, downlink and
// bidirectional (concurrent up+down) directions over the same end-to-end path.
func BenchmarkTransportPush(b *testing.B) {
	for _, scheme := range []string{"tcp", "kcp", "tls", "quic", "ws", "wss"} {
		for _, direction := range []string{"uplink", "downlink", "bidirectional"} {
			b.Run(scheme+"/"+direction+"/payload=1024", func(b *testing.B) {
				transportBench(b, scheme, direction, 1024)
			})
		}
	}
}
