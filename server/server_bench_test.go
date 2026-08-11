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
	srv := newTestServer(NewServerOptions())
	serverSide, clientSide := net.Pipe()
	b.Cleanup(func() {
		serverSide.Close()
		clientSide.Close()
	})

	done := make(chan struct{})
	go func() {
		srv.handleConnection(serverSide)
		close(done)
	}()
	b.Cleanup(func() { <-done })

	if err := sendConnect(clientSide, 0x637a7175, 1, "bench-dev"); err != nil {
		b.Fatal(err)
	}
	if ca := readConnack(b, clientSide); ca.ReturnCode != codec.Accepted {
		b.Fatalf("CONNACK ReturnCode = 0x%x", ca.ReturnCode)
	}

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
