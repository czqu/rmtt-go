package client

import (
	"crypto/tls"
	"net/url"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

func TestNewClientOptions_Defaults(t *testing.T) {
	o := NewClientOptions()
	if o.Servers != nil {
		t.Errorf("Servers = %v, want nil", o.Servers)
	}
	if o.Credential != "" {
		t.Errorf("Credential = %q, want empty", o.Credential)
	}
	if o.Heartbeat != 10 {
		t.Errorf("Heartbeat = %d, want 10", o.Heartbeat)
	}
	if !o.ConnectRetry {
		t.Error("ConnectRetry = false, want true")
	}
	if o.ConnectRetryInterval != 30*time.Second {
		t.Errorf("ConnectRetryInterval = %v, want 30s", o.ConnectRetryInterval)
	}
	if o.ConnectTimeout != 30*time.Second {
		t.Errorf("ConnectTimeout = %v, want 30s", o.ConnectTimeout)
	}
	if !o.AutoReconnect {
		t.Error("AutoReconnect = false, want true")
	}
	if o.MaxReconnectInterval != 10*time.Minute {
		t.Errorf("MaxReconnectInterval = %v, want 10m", o.MaxReconnectInterval)
	}
	if o.ReconnectBase != time.Second {
		t.Errorf("ReconnectBase = %v, want 1s", o.ReconnectBase)
	}
	if o.ReconnectJitter != 0.25 {
		t.Errorf("ReconnectJitter = %v, want 0.25", o.ReconnectJitter)
	}
}

func TestClientOptions_AddServer(t *testing.T) {
	tests := []struct {
		in     string
		scheme string
		host   string
	}{
		{"tcp://127.0.0.1:18883", "tcp", "127.0.0.1:18883"},
		{":18883", "tcp", "127.0.0.1:18883"},
		{"127.0.0.1:18883", "tcp", "127.0.0.1:18883"},
		{"kcp://host:123", "kcp", "host:123"},
		{"ws://127.0.0.1:18886/rmtt", "ws", "127.0.0.1:18886"},
	}
	for _, tt := range tests {
		o := NewClientOptions()
		o.AddServer(tt.in)
		if len(o.Servers) != 1 {
			t.Fatalf("AddServer(%q) Servers len = %d, want 1", tt.in, len(o.Servers))
		}
		if o.Servers[0].Scheme != tt.scheme {
			t.Errorf("AddServer(%q) scheme = %q, want %q", tt.in, o.Servers[0].Scheme, tt.scheme)
		}
		if o.Servers[0].Host != tt.host {
			t.Errorf("AddServer(%q) host = %q, want %q", tt.in, o.Servers[0].Host, tt.host)
		}
	}
}

func TestClientOptions_AddServer_Appends(t *testing.T) {
	o := NewClientOptions()
	o.AddServer("tcp://a:1")
	o.AddServer("tcp://b:2")
	if len(o.Servers) != 2 {
		t.Fatalf("Servers len = %d, want 2", len(o.Servers))
	}
}

func TestClientOptions_SetAdaptiveHeartbeat(t *testing.T) {
	o := NewClientOptions()
	o.SetAdaptiveHeartbeat(10, 300)
	if !o.AdaptiveHeartbeat {
		t.Error("AdaptiveHeartbeat not enabled")
	}
	if o.AdaptiveShort != 10 || o.AdaptiveMax != 300 {
		t.Errorf("AdaptiveShort/AdaptiveMax = %d/%d, want 10/300", o.AdaptiveShort, o.AdaptiveMax)
	}
	if o.ProbeCount != 3 {
		t.Errorf("ProbeCount = %d, want 3", o.ProbeCount)
	}
	if o.ResponseWindow != 2*time.Second {
		t.Errorf("ResponseWindow = %v, want 2s", o.ResponseWindow)
	}
	if o.FineStep != 5 {
		t.Errorf("FineStep = %d, want 5", o.FineStep)
	}
}

func TestClientOptions_SetAdaptiveHeartbeat_Invalid(t *testing.T) {
	o := NewClientOptions()
	o.SetAdaptiveHeartbeat(0, 300)
	if o.AdaptiveHeartbeat {
		t.Error("shortSeconds < 1 must be rejected")
	}
	o2 := NewClientOptions()
	o2.SetAdaptiveHeartbeat(300, 10)
	if o2.AdaptiveHeartbeat {
		t.Error("maxSeconds < shortSeconds must be rejected")
	}
}

func TestClientOptions_SetAdaptiveHeartbeat_KeepsExplicit(t *testing.T) {
	o := NewClientOptions()
	o.SetProbeCount(5)
	o.SetResponseWindow(500 * time.Millisecond)
	o.SetFineStep(2)
	o.SetAdaptiveHeartbeat(10, 300)
	if o.ProbeCount != 5 {
		t.Errorf("ProbeCount = %d, want 5", o.ProbeCount)
	}
	if o.ResponseWindow != 500*time.Millisecond {
		t.Errorf("ResponseWindow = %v, want 500ms", o.ResponseWindow)
	}
	if o.FineStep != 2 {
		t.Errorf("FineStep = %d, want 2", o.FineStep)
	}
}

func TestClientOptions_SetQuicConfig(t *testing.T) {
	o := NewClientOptions()
	cfg := &quic.Config{MaxIdleTimeout: 30 * time.Minute, KeepAlivePeriod: 30 * time.Second}
	o.SetQuicConfig(cfg)
	if o.quicConfig != cfg {
		t.Error("valid config not stored")
	}

	o.SetQuicConfig(nil)
	if o.quicConfig != nil {
		t.Error("nil config must reset to the hardened default")
	}

	bad := &quic.Config{MaxIdleTimeout: 5 * time.Minute, KeepAlivePeriod: 0}
	o.SetQuicConfig(bad)
	if o.quicConfig != nil {
		t.Error("config with KeepAlivePeriod <= 0 must be rejected")
	}
}

func TestClientOptions_Setters(t *testing.T) {
	o := NewClientOptions()

	o.SetCredential("dev-001")
	if o.Credential != "dev-001" {
		t.Errorf("Credential = %q, want dev-001", o.Credential)
	}

	o.SetHeartbeat(5 * time.Second)
	if o.Heartbeat != 5 {
		t.Errorf("Heartbeat = %d, want 5", o.Heartbeat)
	}

	o.SetReconnectBase(2 * time.Second)
	if o.ReconnectBase != 2*time.Second {
		t.Errorf("ReconnectBase = %v, want 2s", o.ReconnectBase)
	}

	o.SetReconnectJitter(0.5)
	if o.ReconnectJitter != 0.5 {
		t.Errorf("ReconnectJitter = %v, want 0.5", o.ReconnectJitter)
	}

	o.SetConnectTimeout(3 * time.Second)
	if o.ConnectTimeout != 3*time.Second {
		t.Errorf("ConnectTimeout = %v, want 3s", o.ConnectTimeout)
	}

	o.SetWriteTimeout(4 * time.Second)
	if o.WriteTimeout != 4*time.Second {
		t.Errorf("WriteTimeout = %v, want 4s", o.WriteTimeout)
	}

	o.SetTlsConfig(&tls.Config{ServerName: "example.com"})
	if o.TLSConfig == nil || o.TLSConfig.ServerName != "example.com" {
		t.Error("TLSConfig not stored")
	}

	o.SetConnectionAttemptHandler(func(*url.URL, *tls.Config) *tls.Config { return nil })
	if o.OnConnectAttempt == nil {
		t.Error("OnConnectAttempt not stored")
	}
}
