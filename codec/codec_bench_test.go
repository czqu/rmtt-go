package codec

import (
	"bytes"
	"io"
	"strconv"
	"testing"
)

// Benchmarks exercise the hot encode/decode path every packet travels
// through. Run with:
//
//	go test -bench=Benchmark -benchmem ./codec/

// makePushFrame encodes a PushPacket with the given payload size and returns
// the raw frame bytes, ready to feed to ReadPacket.
func makePushFrame(payloadSize int) []byte {
	pp := &PushPacket{
		FixedHeader: FixedHeader{MessageType: Push},
		Payload:     bytes.Repeat([]byte("x"), payloadSize),
	}
	var buf bytes.Buffer
	_ = pp.Write(&buf)
	return buf.Bytes()
}

// BenchmarkPushPacketWrite measures encoding a PushPacket (with allocation
// reporting, since each Write builds two intermediate buffers).
func BenchmarkPushPacketWrite(b *testing.B) {
	for _, size := range []int{64, 1024, 4096} {
		b.Run("payload="+strconv.Itoa(size), func(b *testing.B) {
			pp := &PushPacket{
				FixedHeader: FixedHeader{MessageType: Push},
				Payload:     bytes.Repeat([]byte("x"), size),
			}
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := pp.Write(io.Discard); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPushPacketRead measures decoding a full PushPacket from a wire
// frame, the path server/client use on every inbound message.
func BenchmarkPushPacketRead(b *testing.B) {
	for _, size := range []int{64, 1024, 4096} {
		b.Run("payload="+strconv.Itoa(size), func(b *testing.B) {
			frame := makePushFrame(size)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cp, err := ReadPacket(bytes.NewReader(frame))
				if err != nil {
					b.Fatal(err)
				}
				if _, ok := cp.(*PushPacket); !ok {
					b.Fatalf("unexpected packet type %T", cp)
				}
			}
		})
	}
}

// BenchmarkReadPacketThroughput measures raw decode throughput over a large byte
// stream, isolating frame parsing from per-iteration allocation noise.
func BenchmarkReadPacketThroughput(b *testing.B) {
	frame := makePushFrame(1024)
	var stream bytes.Buffer
	for i := 0; i < 256; i++ {
		stream.Write(frame)
	}
	data := stream.Bytes()

	b.SetBytes(int64(len(frame)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := bytes.NewReader(data)
		for j := 0; j < 256; j++ {
			cp, err := ReadPacket(r)
			if err != nil {
				b.Fatal(err)
			}
			_ = cp
		}
	}
}
