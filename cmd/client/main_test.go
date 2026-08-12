package main

import (
	"testing"
	"time"
)

func TestBuildOptions(t *testing.T) {
	opts := buildOptions("tcp://127.0.0.1:18883", "test-device")
	if len(opts.Servers) != 1 {
		t.Fatalf("Servers = %d entries, want 1", len(opts.Servers))
	}
	if opts.Servers[0].Scheme != "tcp" {
		t.Errorf("Scheme = %q, want tcp", opts.Servers[0].Scheme)
	}
	if opts.Credential != "test-device" {
		t.Errorf("Credential = %q, want test-device", opts.Credential)
	}
	if opts.ConnectTimeout != 5*time.Second {
		t.Errorf("ConnectTimeout = %v, want 5s", opts.ConnectTimeout)
	}
	if !opts.AutoReconnect {
		t.Error("AutoReconnect = false, want true")
	}
}
