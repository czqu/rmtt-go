// End-to-end benchmarks over a real TCP connection between the library's own
// client and server. Run with:
//
//	go test -bench=Benchmark -benchmem ./integration/
package integration_test

import (
	"fmt"
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
