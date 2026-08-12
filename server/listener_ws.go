package server

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"golang.org/x/net/websocket"
)

type wsListener struct {
	addr   string
	path   string
	config *tls.Config
	srv    *http.Server
}

// NewWSListener returns a WebSocket listener bound to addr (e.g. ":18886").
// path is the upgrade endpoint (defaults to "/rmtt" when empty).
func NewWSListener(addr, path string) Listener {
	return &wsListener{addr: addr, path: path}
}

// NewWSSListener returns a secure WebSocket listener bound to addr.
func NewWSSListener(addr, path string, config *tls.Config) Listener {
	return &wsListener{addr: addr, path: path, config: config}
}

func (l *wsListener) Serve(ctx context.Context, handler func(net.Conn)) error {
	path := l.path
	if path == "" {
		path = "/rmtt"
	}

	ws := &websocket.Server{
		Handshake: func(cfg *websocket.Config, req *http.Request) error {
			cfg.Version = websocket.ProtocolVersionHybi13
			cfg.Protocol = []string{"rmtt"}
			return nil
		},
		Handler: func(conn *websocket.Conn) {
			handler(&wsConn{ws: conn})
		},
	}

	mux := http.NewServeMux()
	mux.Handle(path, ws)

	ln, err := net.Listen("tcp", l.addr)
	if err != nil {
		return err
	}
	if l.config != nil {
		ln = tls.NewListener(ln, l.config)
	}

	srv := &http.Server{Handler: mux}
	l.srv = srv

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return ctx.Err()
		}
		return err
	}
}

func (l *wsListener) Close() error {
	if l.srv != nil {
		// Match Serve's ctx-cancel path: a graceful Shutdown lets in-flight
		// websocket handshakes finish (up to 5s) instead of hard-closing
		// mid-handshake, which previously left clients with a bare EOF.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return l.srv.Shutdown(ctx)
	}
	return nil
}
