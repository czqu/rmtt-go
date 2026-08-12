package client

import "testing"

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
