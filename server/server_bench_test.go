package server

import (
	"bytes"
	"net"
	"strconv"
	"testing"

	"github.com/czqu/rmtt-go/codec"
)

// Benchmarks exercise the server-to-client downlink push path (NewServer.Push
// -> deviceConnection.Write -> codec encode). Run with:
//
//	go test -bench=Benchmark -benchmem ./server/

// startBenchConnection opens a net.Pipe-backed connection, runs the server
// handshake on it, and returns the server handle. The client end is drained in
// a background goroutine so the server's writes never block.
func startBenchConnection(b *testing.B) *serverImpl {
	b.Helper()
	// Disable the server-side keepalive reaper: the benchmark only pushes
	// downlink and never sends another client packet, so an enabled reaper
	// (default 60s * 1.5 = 90s) would stall every sub-benchmark's teardown.
	srv := newTestServer(NewServerOptions().SetKeepalivePolicy(&KeepalivePolicy{AllowDisable: true}))
	serverSide, clientSide := net.Pipe()

	done := make(chan struct{})
	go func() {
		srv.handleConnection(serverSide)
		close(done)
	}()
	// b.Cleanup runs LIFO, so close the pipes first (unblocking the server's
	// read loop) before waiting on handleConnection to return. Ordering this
	// two separate Cleanup calls would wait on done before closing, hanging.
	b.Cleanup(func() {
		serverSide.Close()
		clientSide.Close()
		<-done
	})

	if err := sendConnect(clientSide, 0x637a7175, 1, "bench-dev"); err != nil {
		b.Fatal(err)
	}
	if ca := readConnack(b, clientSide); ca.ReturnCode != codec.Accepted {
		b.Fatalf("CONNACK ReturnCode = 0x%x", ca.ReturnCode)
	}
	// The server writes CONNACK before registering the device in its store, so
	// reading CONNACK alone does not guarantee Get("bench-dev") succeeds. Wait
	// for registration, otherwise the first Push can race ahead and fail with
	// "device bench-dev not connected" (flaky, especially for larger payloads).
	waitRegistered(b, srv, "bench-dev")

	// Drain the client side in the background so the server's writes are not
	// held up by our (very fast) benchmark loop.
	go func() {
		for {
			if _, err := codec.ReadPacket(clientSide); err != nil {
				return
			}
		}
	}()
	return srv
}

// BenchmarkServerPush measures the downlink push path (server -> client) for a
// range of payload sizes, reporting bytes/sec and allocations/op.
func BenchmarkServerPush(b *testing.B) {
	for _, size := range []int{64, 1024, 4096} {
		b.Run("payload="+strconv.Itoa(size), func(b *testing.B) {
			srv := startBenchConnection(b)
			payload := bytes.Repeat([]byte("y"), size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := srv.Push("bench-dev", payload); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
