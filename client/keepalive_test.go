package client

import (
	"net"
	"testing"
	"time"

	"github.com/czqu/rmtt-go/codec"
)

func TestEffectiveHeartbeat(t *testing.T) {
	c := &client{options: ClientOptions{Heartbeat: 10}}
	if got := c.effectiveHeartbeat(); got != 10 {
		t.Errorf("effectiveHeartbeat() = %d, want 10", got)
	}

	c.serverKp.Store(30)
	if got := c.effectiveHeartbeat(); got != 30 {
		t.Errorf("effectiveHeartbeat() with serverKp = %d, want 30", got)
	}

	c.serverKp.Store(0)
	if got := c.effectiveHeartbeat(); got != 10 {
		t.Errorf("effectiveHeartbeat() after serverKp reset = %d, want 10", got)
	}
}

// TestFixedKeepalive_PingGapInsideReapWindow asserts that consecutive
// PINGREQs at heartbeat=1s stay inside the server's 1.5x reap window. The
// check ticker's 1s floor made sends land exactly on their own deadline, so
// any nonzero write latency failed the ">=" test at that tick and the next
// send came a full tick (2s) later — the server reaped the connection first
// (e2e blackbox F8).
func TestFixedKeepalive_PingGapInsideReapWindow(t *testing.T) {
	srvEnd, cliEnd := net.Pipe()
	defer srvEnd.Close()
	defer cliEnd.Close()

	c := &client{options: ClientOptions{Heartbeat: 1}}
	c.stop = make(chan struct{})
	c.lastSent.Store(time.Now())
	c.lastReceived.Store(time.Now())
	defer close(c.stop)

	go fixedKeepalive(c, cliEnd)

	readPing := func() time.Time {
		t.Helper()
		_ = srvEnd.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
		cp, err := codec.ReadPacket(srvEnd)
		if err != nil {
			t.Fatalf("read PINGREQ: %v", err)
		}
		if _, ok := cp.(*codec.PingreqPacket); !ok {
			t.Fatalf("got %T, want *PingreqPacket", cp)
		}
		// Mimic the comms loop: inbound traffic refreshes lastReceived.
		c.lastReceived.Store(time.Now())
		return time.Now()
	}

	first := readPing()
	second := readPing()
	if gap := second.Sub(first); gap >= 1500*time.Millisecond {
		t.Fatalf("PINGREQ gap = %v, must stay inside the server's 1.5s reap window", gap)
	}
}
