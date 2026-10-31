// Black-box test server for the Go rmtt implementation.
//
// It is a thin wrapper around the real rmtt-go server library: it adds CLI
// transport selection, keepalive policy / auth-reject knobs and a localhost
// line-based control socket so the external test driver can trigger
// server-side APIs (push / kick / online / shutdown) that a client cannot
// invoke over the protocol.
package main

import (
	"bufio"
	"crypto/tls"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/czqu/rmtt-go/server"
)

type allowExcept struct{ reject map[string]bool }

func (a allowExcept) Authenticate(credential string) (string, bool) {
	if a.reject[credential] {
		return "", false
	}
	return credential, true
}

func main() {
	transport := flag.String("transport", "tcp", "tcp|tls|ws|wss|kcp|quic")
	port := flag.Int("port", 19990, "listener port")
	control := flag.Int("control", 0, "control socket port (0 = disabled)")
	cert := flag.String("cert", "", "TLS certificate (PEM)")
	key := flag.String("key", "", "TLS private key (PEM)")
	wsPath := flag.String("ws-path", "/rmtt", "WebSocket path")
	minKp := flag.Int64("keepalive-min", 30, "keepalive policy minimum seconds")
	maxKp := flag.Int64("keepalive-max", 600, "keepalive policy maximum seconds")
	defKp := flag.Int64("keepalive-default", 60, "keepalive fallback seconds")
	allowDisable := flag.Bool("allow-disable", false, "allow client keepalive=0 to disable")
	reject := flag.String("reject", "", "comma separated credentials to reject")
	noEcho := flag.Bool("no-echo", false, "do not echo upstream pushes")
	flag.Parse()

	opts := server.NewServerOptions()
	opts.SetKeepalivePolicy(&server.KeepalivePolicy{
		MinSeconds: *minKp, MaxSeconds: *maxKp, DefaultSeconds: *defKp, AllowDisable: *allowDisable,
	})
	rej := map[string]bool{}
	for _, c := range strings.Split(*reject, ",") {
		if c != "" {
			rej[c] = true
		}
	}
	opts.SetAuthenticator(allowExcept{rej})

	var tlsCfg *tls.Config
	if *cert != "" && *key != "" {
		pair, err := tls.LoadX509KeyPair(*cert, *key)
		if err != nil {
			fmt.Fprintln(os.Stderr, "load cert:", err)
			os.Exit(2)
		}
		tlsCfg = &tls.Config{Certificates: []tls.Certificate{pair}}
	}

	addr := fmt.Sprintf(":%d", *port)
	switch *transport {
	case "tcp":
		opts.AddListener(server.NewTCPListener(addr))
	case "tls":
		opts.AddListener(server.NewTLSListener(addr, tlsCfg))
	case "ws":
		opts.AddListener(server.NewWSListener(addr, *wsPath))
	case "wss":
		opts.AddListener(server.NewWSSListener(addr, *wsPath, tlsCfg))
	case "kcp":
		opts.AddListener(server.NewKCPListener(addr))
	case "quic":
		quicCfg := tlsCfg.Clone()
		quicCfg.NextProtos = []string{"rmtt"}
		opts.AddListener(server.NewQUICListener(addr, quicCfg))
	default:
		fmt.Fprintln(os.Stderr, "unknown transport", *transport)
		os.Exit(2)
	}

	var srv server.Server
	opts.SetMessageHandler(func(deviceID string, payload []byte) {
		fmt.Printf("PUSH device=%s len=%d payload=%q\n", deviceID, len(payload), truncate(payload, 120))
		os.Stdout.Sync()
		if !*noEcho {
			echo := append([]byte("echo:"), payload...)
			if err := srv.Push(deviceID, echo); err != nil {
				fmt.Fprintf(os.Stderr, "echo push to %s failed: %v\n", deviceID, err)
			}
		}
	})
	ev := &events{online: map[string]bool{}}
	opts.SetConnectionListener(ev)
	srv = server.NewServer(opts)

	if *control > 0 {
		go controlLoop(*control, srv, ev)
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil {
			fmt.Fprintln(os.Stderr, "server error:", err)
		}
	}()
	time.Sleep(150 * time.Millisecond)
	fmt.Printf("READY transport=%s port=%d control=%d pid=%d\n", *transport, *port, *control, os.Getpid())
	os.Stdout.Sync()
	select {}
}

type events struct {
	mu     sync.Mutex
	online map[string]bool
}

func (e *events) OnConnectionEstablished(deviceID string) {
	e.mu.Lock()
	e.online[deviceID] = true
	e.mu.Unlock()
	fmt.Printf("CONNECTED device=%s\n", deviceID)
	os.Stdout.Sync()
}

func (e *events) OnConnectionClosed(deviceID, reason string) {
	e.mu.Lock()
	delete(e.online, deviceID)
	e.mu.Unlock()
	fmt.Printf("DISCONNECTED device=%s reason=%s\n", deviceID, reason)
	os.Stdout.Sync()
}

func (e *events) isOnline(deviceID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.online[deviceID]
}

func truncate(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

func controlLoop(port int, srv server.Server, ev *events) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		fmt.Fprintln(os.Stderr, "control listen:", err)
		os.Exit(2)
	}
	fmt.Printf("CONTROL_READY port=%d\n", port)
	os.Stdout.Sync()
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			sc := bufio.NewScanner(c)
			for sc.Scan() {
				line := strings.TrimSpace(sc.Text())
				if line == "" {
					continue
				}
				fmt.Printf("CONTROL %s\n", line)
				os.Stdout.Sync()
				fields := strings.SplitN(line, " ", 3)
				switch fields[0] {
				case "PING":
					fmt.Fprintln(c, "OK PONG")
				case "PUSH":
					if len(fields) < 3 {
						fmt.Fprintln(c, "ERR usage PUSH <device> <text>")
						continue
					}
					if err := srv.Push(fields[1], []byte(fields[2])); err != nil {
						fmt.Fprintln(c, "ERR", err)
					} else {
						fmt.Fprintln(c, "OK")
					}
				case "KICK":
					if len(fields) < 3 {
						fmt.Fprintln(c, "ERR usage KICK <device> <code>")
						continue
					}
					code, err := strconv.Atoi(fields[2])
					if err != nil {
						fmt.Fprintln(c, "ERR code")
						continue
					}
					if err := srv.Kick(fields[1], byte(code)); err != nil {
						fmt.Fprintln(c, "ERR", err)
					} else {
						fmt.Fprintln(c, "OK")
					}
				case "ONLINE":
					if len(fields) < 2 {
						fmt.Fprintln(c, "ERR usage ONLINE <device>")
						continue
					}
					fmt.Fprintf(c, "OK %v\n", ev.isOnline(fields[1]))
				case "SHUTDOWN":
					fmt.Fprintln(c, "OK")
					c.Close()
					_ = srv.Close()
					os.Exit(0)
				default:
					fmt.Fprintln(c, "ERR unknown")
				}
			}
		}(conn)
	}
}
