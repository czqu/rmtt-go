package server

import (
	"context"
	"net"

	"github.com/xtaci/kcp-go"
)

type kcpListener struct {
	addr string
	ln   net.Listener
}

// NewKCPListener returns a KCP (reliable UDP) listener bound to addr (e.g. ":18883").
func NewKCPListener(addr string) Listener {
	return &kcpListener{addr: addr}
}

func (l *kcpListener) Serve(ctx context.Context, handler func(net.Conn)) error {
	ln, err := kcp.Listen(l.addr)
	if err != nil {
		return err
	}
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

func (l *kcpListener) Close() error {
	if l.ln != nil {
		return l.ln.Close()
	}
	return nil
}
