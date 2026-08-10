package server

import (
	"context"
	"crypto/tls"
	"net"
)

type tlsListener struct {
	addr   string
	config *tls.Config
	ln     net.Listener
}

// NewTLSListener returns a TLS-over-TCP listener bound to addr (e.g. ":18884").
func NewTLSListener(addr string, config *tls.Config) Listener {
	return &tlsListener{addr: addr, config: config}
}

func (l *tlsListener) Serve(ctx context.Context, handler func(net.Conn)) error {
	inner, err := net.Listen("tcp", l.addr)
	if err != nil {
		return err
	}
	ln := tls.NewListener(inner, l.config)
	l.ln = ln

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				return err
			}
		}
		go handler(conn)
	}
}

func (l *tlsListener) Close() error {
	if l.ln != nil {
		return l.ln.Close()
	}
	return nil
}
