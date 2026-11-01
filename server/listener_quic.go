package server

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"time"

	"github.com/czqu/rmtt-go/codec"
	"github.com/quic-go/quic-go"
)

type quicListener struct {
	addr     string
	config   *tls.Config
	quicConf *quic.Config

	// mu guards ln, which Serve writes once the socket is bound and Close (and
	// callers polling readiness) read concurrently.
	mu sync.Mutex
	ln *quic.Listener
}

// setLn records the bound QUIC listener.
func (l *quicListener) setLn(ln *quic.Listener) {
	l.mu.Lock()
	l.ln = ln
	l.mu.Unlock()
}

// boundLn returns the bound QUIC listener, or nil if Serve has not bound yet.
func (l *quicListener) boundLn() *quic.Listener {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.ln
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
//
// ALPN is governed by the supplied tls.Config's NextProtos (see
// codec.WithDefaultALPN): when NextProtos is unset the listener advertises the
// default "rmtt" token so it is reachable by the Java/Netty reference
// implementation, which requires the server to select "rmtt"; when NextProtos is
// set it is honoured verbatim, so callers can override the token or disable ALPN
// (empty slice) for legacy peers that send none. The config is only read, never
// mutated.
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
	ln, err := quic.ListenAddr(l.addr, codec.WithDefaultALPN(l.config), chooseQuicConf(l.quicConf))
	if err != nil {
		return err
	}
	l.setLn(ln)

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
// is treated as one rmtt device connection. The session is owned by this
// goroutine: when AcceptStream stops (peer closed, listener shutdown, or
// context cancel) the session is closed once here so its UDP socket and
// goroutines are released — individual stream conns only close their own
// stream (see quicStreamConn.Close).
func (l *quicListener) serveSession(ctx context.Context, session *quic.Conn, handler func(net.Conn)) {
	defer func() {
		_ = session.CloseWithError(0, "")
	}()
	for {
		stream, err := session.AcceptStream(ctx)
		if err != nil {
			return
		}
		go handler(&quicStreamConn{Stream: stream, session: session})
	}
}

func (l *quicListener) Close() error {
	if ln := l.boundLn(); ln != nil {
		return ln.Close()
	}
	return nil
}
