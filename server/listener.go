package server

import (
	"context"
	"net"
)

// Listener abstracts a transport acceptor. Serve blocks until ctx is cancelled
// or an irrecoverable error occurs, invoking handler for each accepted connection.
type Listener interface {
	Serve(ctx context.Context, handler func(net.Conn)) error
	Close() error
}

// serveAccepted runs the shared accept loop for stream listeners: it closes ln
// when ctx is cancelled and handles each accepted connection in its own
// goroutine until Accept fails.
func serveAccepted(ctx context.Context, ln net.Listener, handler func(net.Conn)) error {
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

type tcpListener struct {
	addr string
	ln   net.Listener
}

// NewTCPListener returns a plain TCP listener bound to addr (e.g. ":18883").
func NewTCPListener(addr string) Listener {
	return &tcpListener{addr: addr}
}

func (tl *tcpListener) Serve(ctx context.Context, handler func(net.Conn)) error {
	ln, err := net.Listen("tcp", tl.addr)
	if err != nil {
		return err
	}
	tl.ln = ln
	return serveAccepted(ctx, ln, handler)
}

func (tl *tcpListener) Close() error {
	if tl.ln != nil {
		return tl.ln.Close()
	}
	return nil
}
