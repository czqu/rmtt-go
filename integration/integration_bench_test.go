// End-to-end benchmarks over a real TCP connection between the library's own
// client and server. Run with:
//
//	go test -bench=Benchmark -benchmem ./integration/
package integration_test

import (
	"errors"
	"fmt"
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
