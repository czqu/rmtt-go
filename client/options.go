package client

import (
	"crypto/tls"
	"net/url"
	"strings"
	"time"

	"github.com/quic-go/quic-go"
)

type ConnectionAttemptHandler func(server *url.URL, tlsCfg *tls.Config) *tls.Config
type ConnectionLostHandler func(Client, error)
type ReconnectHandler func(Client, *ClientOptions)

type ClientOptions struct {
	Servers              []*url.URL
	Credential           string
	Heartbeat            int64
	ProtocolVersion      uint
	ConnectRetry         bool
	ConnectRetryInterval time.Duration
	ConnectTimeout       time.Duration
	WriteTimeout         time.Duration
	AutoReconnect        bool
	OnConnectionLost     ConnectionLostHandler
	MaxReconnectInterval time.Duration
	OnReconnecting       ReconnectHandler
	TLSConfig            *tls.Config
	OnConnectAttempt     ConnectionAttemptHandler
	ReconnectBase        time.Duration
	ReconnectJitter      float64

	// Adaptive heartbeat (spec §6.5, client-side policy). When enabled, the client probes the
	// maximum sustainable heartbeat interval within [AdaptiveShort, AdaptiveMax] (capped by the
	// negotiated server_kp from CONNACK) and settles at ~90% of the found maximum. The CONNECT
	// Keepalive proposal becomes AdaptiveMax. Mutually exclusive with a fixed Heartbeat.
	AdaptiveHeartbeat bool
	AdaptiveShort     int64 // seconds
	AdaptiveMax       int64 // seconds
	ProbeCount        int
	ResponseWindow    time.Duration
	FineStep          int64 // seconds

	// quicConfig overrides the QUIC transport settings for "quic://" servers.
	// nil (the default) uses the library's hardened defaults
	// (MaxIdleTimeout 15min, KeepAlivePeriod 30s). Any supplied Config with
	// KeepAlivePeriod <= 0 is rejected by SetQuicConfig and the safe default is
	// kept instead — see SetQuicConfig for the full warning.
	quicConfig *quic.Config
}

func (o *ClientOptions) AddServer(server string) *ClientOptions {
	if len(server) > 0 && server[0] == ':' {
		server = "127.0.0.1" + server
	}
	if !strings.Contains(server, "://") {
		server = "tcp://" + server
	}
	serverURI, err := url.Parse(server)
	if err != nil {
		ERROR.Println("Failed to parse address: %s", server, err)
		return o
	}
	o.Servers = append(o.Servers, serverURI)
	return o
}

func (o *ClientOptions) SetCredential(id string) *ClientOptions {
	o.Credential = id
	return o
}

func (o *ClientOptions) SetHeartbeat(k time.Duration) *ClientOptions {
	o.Heartbeat = int64(k / time.Second)
	return o
}

// SetAdaptiveHeartbeat enables adaptive heartbeat (spec §6.5). The client probes the maximum
// sustainable heartbeat interval within [shortSeconds, maxSeconds] (capped by the negotiated
// server_kp from CONNACK) and settles at ~90% of the found maximum. Replaces a fixed Heartbeat:
// the CONNECT Keepalive proposal becomes maxSeconds. Incompatible with SetHeartbeat.
func (o *ClientOptions) SetAdaptiveHeartbeat(shortSeconds, maxSeconds int64) *ClientOptions {
	if shortSeconds < 1 {
		ERROR.Println("SetAdaptiveHeartbeat: shortSeconds must be >= 1")
		return o
	}
	if maxSeconds < shortSeconds {
		ERROR.Println("SetAdaptiveHeartbeat: maxSeconds must be >= shortSeconds")
		return o
	}
	o.AdaptiveHeartbeat = true
	o.AdaptiveShort = shortSeconds
	o.AdaptiveMax = maxSeconds
	if o.ProbeCount == 0 {
		o.ProbeCount = 3
	}
	if o.ResponseWindow == 0 {
		o.ResponseWindow = 2 * time.Second
	}
	if o.FineStep == 0 {
		o.FineStep = 5
	}
	return o
}

// SetProbeCount sets the number of consecutive successful short heartbeats required before the
// probing phase starts (default 3). Only meaningful with SetAdaptiveHeartbeat.
func (o *ClientOptions) SetProbeCount(n int) *ClientOptions {
	o.ProbeCount = n
	return o
}

// SetResponseWindow sets the maximum wait for a PINGRESP before counting a probe as failed
// (default 2s). Only meaningful with SetAdaptiveHeartbeat.
func (o *ClientOptions) SetResponseWindow(d time.Duration) *ClientOptions {
	o.ResponseWindow = d
	return o
}

// SetFineStep sets the nudge step (seconds) used in the fine-tuning probing phase (default 5).
// Only meaningful with SetAdaptiveHeartbeat.
func (o *ClientOptions) SetFineStep(seconds int64) *ClientOptions {
	o.FineStep = seconds
	return o
}

func (o *ClientOptions) SetReconnectBase(k time.Duration) *ClientOptions {
	o.ReconnectBase = k
	return o
}

func (o *ClientOptions) SetReconnectJitter(j float64) *ClientOptions {
	o.ReconnectJitter = j
	return o
}

func (o *ClientOptions) SetConnectTimeout(k time.Duration) *ClientOptions {
	o.ConnectTimeout = k
	return o
}

func (o *ClientOptions) SetWriteTimeout(k time.Duration) *ClientOptions {
	o.WriteTimeout = k
	return o
}

func (o *ClientOptions) SetTlsConfig(config *tls.Config) *ClientOptions {
	o.TLSConfig = config
	return o
}

// SetQuicConfig overrides the QUIC transport settings used for "quic://"
// servers. Pass nil to restore the library's hardened defaults
// (MaxIdleTimeout 15min, KeepAlivePeriod 30s).
//
// WARNING: you take full responsibility for your own values. A Config with
// KeepAlivePeriod <= 0 (which disables transport-level keepalive) or a
// MaxIdleTimeout shorter than the application's heartbeat/report interval will
// reproduce the classic periodic "timeout: no recent network activity" drops
// whenever the adaptive heartbeat grows beyond the idle window — that is the
// exact bug this library ships its safe defaults to prevent. For your own
// safety, SetQuicConfig rejects KeepAlivePeriod <= 0 and falls back to the
// hardened default rather than letting the connection die silently.
func (o *ClientOptions) SetQuicConfig(config *quic.Config) *ClientOptions {
	if config == nil {
		o.quicConfig = nil
		return o
	}
	if config.KeepAlivePeriod <= 0 {
		ERROR.Println("SetQuicConfig: KeepAlivePeriod must be > 0; " +
			"refusing unsafe config, keeping hardened default (30s keepalive / 15min idle)")
		o.quicConfig = nil
		return o
	}
	o.quicConfig = config
	return o
}

func (o *ClientOptions) SetConnectionAttemptHandler(onConnectAttempt ConnectionAttemptHandler) *ClientOptions {
	o.OnConnectAttempt = onConnectAttempt
	return o
}

func NewClientOptions() *ClientOptions {
	o := &ClientOptions{
		Servers:              nil,
		Credential:           "",
		Heartbeat:            10,
		ProtocolVersion:      0,
		ConnectRetry:         true,
		ConnectRetryInterval: 30 * time.Second,
		ConnectTimeout:       30 * time.Second,
		AutoReconnect:        true,
		OnConnectionLost:     nil,
		MaxReconnectInterval: 10 * time.Minute,
		OnReconnecting:       nil,
		OnConnectAttempt:     nil,
		ReconnectBase:        1 * time.Second,
		ReconnectJitter:      0.25,
	}
	return o
}
