package server

import (
	"context"
	"crypto/tls"
	"net"
	"time"

	"github.com/quic-go/quic-go"
)

type quicListener struct {
	addr     string
	config   *tls.Config
	quicConf *quic.Config
	ln       *quic.Listener
}

// quicConf returns the shared quic-go transport configuration. The library's
// adaptive heartbeat can stretch far beyond quic-go's 30s default idle timeout
// (heartbeats up to 600s are allowed), so that default would drop otherwise idle but
// still-live connections. KeepAlivePeriod forces a lightweight interval packet
// so the QUIC idle timer never fires while an application heartbeat is in
// between longer gaps; MaxIdleTimeout is a generous hard cap.
//
// Per-listener overrides are allowed via NewQUICListenerWithConfig, but the
// caller takes full responsibility for their values: a Config with
// KeepAlivePeriod <= 0 or MaxIdleTimeout shorter than the application's
// heartbeat/report interval reproduces the periodic "connection closed" drops.
// KeepAlivePeriod <= 0 is rejected outright (falls back to the hardened
// default); a too-short MaxIdleTimeout is the caller's risk.
func defaultQuicConf() *quic.Config {
	return &quic.Config{
		MaxIdleTimeout:  15 * time.Minute,
		KeepAlivePeriod: 30 * time.Second,
	}
}

// chooseQuicConf picks the active quic config for a listener.
func chooseQuicConf(override *quic.Config) *quic.Config {
	if override != nil {
		return override
	}
	return defaultQuicConf()
}

// NewQUICListener returns a QUIC (UDP + TLS 1.3) listener bound to addr (e.g. ":18885"),
// using the library's hardened transport defaults.
func NewQUICListener(addr string, config *tls.Config) Listener {
	return &quicListener{addr: addr, config: config}
}

// NewQUICListenerWithConfig returns a QUIC listener with an explicit quic-go
// transport config. Passing nil keeps the hardened defaults. A supplied Config
// with KeepAlivePeriod <= 0 is rejected (the hardened default is used instead)
// because disabling transport-level keepalive would let otherwise live idle
// connections be torn down — see defaultQuicConf for the full warning.
func NewQUICListenerWithConfig(addr string, config *tls.Config, quicCfg *quic.Config) Listener {
	if quicCfg != nil && quicCfg.KeepAlivePeriod <= 0 {
		ERROR.Println("NewQUICListenerWithConfig: KeepAlivePeriod must be > 0; " +
			"refusing unsafe config, keeping hardened default (30s keepalive / 15min idle)")
		quicCfg = nil
	}
	return &quicListener{addr: addr, config: config, quicConf: quicCfg}
}

func (l *quicListener) Serve(ctx context.Context, handler func(net.Conn)) error {
	ln, err := quic.ListenAddr(l.addr, l.config, chooseQuicConf(l.quicConf))
	if err != nil {
		return err
	}
	l.ln = ln

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept(ctx)
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return err
			}
		}
		go l.serveSession(ctx, conn, handler)
	}
}

// serveSession accepts bidirectional streams from a QUIC connection; each stream
// is treated as one rmtt device connection.
func (l *quicListener) serveSession(ctx context.Context, session quic.Connection, handler func(net.Conn)) {
	for {
		stream, err := session.AcceptStream(ctx)
		if err != nil {
			return
		}
		go handler(&quicStreamConn{Stream: stream, session: session})
	}
}

func (l *quicListener) Close() error {
	if l.ln != nil {
		return l.ln.Close()
	}
	return nil
}
