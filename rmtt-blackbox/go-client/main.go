// Black-box scenario driver for the Go rmtt client library.
//
// Every scenario prints machine-readable event lines (CONNECT / ECHO / RECV /
// LOST / RECONNECTED / ...) and finishes with "RESULT PASS" + exit 0 on
// success, or "RESULT FAIL <reason>" + exit 1.
package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"flag"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/czqu/rmtt-go/client"
)

var (
	transport  = flag.String("transport", "tcp", "tcp|tls|ws|wss|kcp|quic")
	host       = flag.String("host", "127.0.0.1", "server host")
	port       = flag.Int("port", 19990, "server port")
	credential = flag.String("credential", "go-dev", "credential")
	keepalive  = flag.Int("keepalive", 30, "keepalive proposal seconds")
	scenario   = flag.String("scenario", "basic", "basic|concurrent|large|binary|hold|kick|takeover-attacker|reconnect|keepalive|downlink|authreject")
	count      = flag.Int("count", 5, "message count for concurrent")
	size       = flag.Int("size", 262144, "payload size for large")
	waitSec    = flag.Int("wait", 15, "scenario timeout seconds")
	reconnect  = flag.Bool("reconnect", false, "enable auto reconnect")
	caFile     = flag.String("ca", "", "CA certificate for TLS")
)

var echoes = make(chan []byte, 1024)
var lost = make(chan struct{}, 8)

func main() {
	flag.Parse()
	opts := client.NewClientOptions()
	scheme := *transport
	addr := fmt.Sprintf("%s://%s:%d", scheme, *host, *port)
	if scheme == "ws" || scheme == "wss" {
		addr += "/rmtt"
	}
	opts.AddServer(addr)
	opts.SetCredential(*credential)
	opts.SetHeartbeat(time.Duration(*keepalive) * time.Second)
	opts.SetConnectTimeout(5 * time.Second)
	opts.AutoReconnect = *reconnect
	opts.ConnectRetry = *reconnect
	opts.ReconnectBase = 200 * time.Millisecond
	opts.MaxReconnectInterval = 1 * time.Second
	opts.ReconnectJitter = 0

	if scheme == "tls" || scheme == "wss" || scheme == "quic" {
		tlsCfg := &tls.Config{InsecureSkipVerify: true}
		if scheme == "quic" {
			tlsCfg.NextProtos = []string{"rmtt"}
		}
		if *caFile != "" {
			pem, err := os.ReadFile(*caFile)
			if err != nil {
				fail("read ca: %v", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(pem) {
				fail("bad ca")
			}
			tlsCfg.RootCAs = pool
			tlsCfg.InsecureSkipVerify = false
		}
		opts.TLSConfig = tlsCfg
	}

	opts.OnConnectionLost = func(_ client.Client, err error) {
		fmt.Printf("EVENT LOST reason=%v\n", err)
		os.Stdout.Sync()
		select {
		case lost <- struct{}{}:
		default:
		}
	}
	c := client.NewClient(opts)
	c.AddPayloadHandlerLast(func(_ client.Client, m client.Message) {
		select {
		case echoes <- m.Payload():
		default:
		}
	})
	connect := func() bool {
		t := c.Connect()
		if !t.WaitTimeout(time.Duration(*waitSec) * time.Second) {
			fail("connect timeout")
		}
		if t.Error() != nil {
			fmt.Printf("EVENT CONNECT_ERROR %v\n", t.Error())
			os.Stdout.Sync()
			return false
		}
		fmt.Println("EVENT CONNECTED")
		os.Stdout.Sync()
		return true
	}

	switch *scenario {
	case "basic":
		if !connect() {
			fail("connect rejected")
		}
		msg := []byte("hello-from-go")
		push(c, msg)
		want(echoFor(msg))
		finish(c)
	case "concurrent":
		if !connect() {
			fail("connect rejected")
		}
		n := *count
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if t := c.Push(fmt.Sprintf("msg-%d", i)); t.WaitTimeout(5*time.Second) && t.Error() != nil {
					fail("push %d: %v", i, t.Error())
				}
			}(i)
		}
		wg.Wait()
		got := map[string]bool{}
		deadline := time.After(10 * time.Second)
		for len(got) < n {
			select {
			case p := <-echoes:
				got[string(p)] = true
			case <-deadline:
				fail("got %d/%d echoes", len(got), n)
			}
		}
		for i := 0; i < n; i++ {
			if !got["echo:msg-"+fmt.Sprint(i)] {
				fail("missing echo msg-%d", i)
			}
		}
		fmt.Printf("EVENT ECHO_OK count=%d\n", n)
		finish(c)
	case "large", "binary":
		if !connect() {
			fail("connect rejected")
		}
		var payload []byte
		if *scenario == "large" {
			payload = bytes.Repeat([]byte("0123456789abcdef"), *size/16)
		} else {
			payload = make([]byte, 256)
			for i := range payload {
				payload[i] = byte(i)
			}
		}
		push(c, payload)
		want(echoFor(payload))
		finish(c)
	case "downlink":
		if !connect() {
			fail("connect rejected")
		}
		select {
		case p := <-echoes:
			fmt.Printf("EVENT RECV len=%d payload=%q\n", len(p), truncate(p, 80))
			fmt.Println("RESULT PASS")
		case <-time.After(time.Duration(*waitSec) * time.Second):
			fail("no downstream push")
		}
		c.Disconnect(100)
	case "hold":
		if !connect() {
			fail("connect rejected")
		}
		fmt.Println("EVENT READY")
		os.Stdout.Sync()
		time.Sleep(time.Duration(*waitSec) * time.Second)
		if !c.IsConnected() {
			fail("connection lost while holding")
		}
		fmt.Println("EVENT STILL_CONNECTED")
		fmt.Println("RESULT PASS")
		c.Disconnect(100)
	case "kick", "takeover-attacker":
		if !connect() {
			fail("connect rejected")
		}
		fmt.Println("EVENT READY")
		os.Stdout.Sync()
		if *scenario == "takeover-attacker" {
			time.Sleep(time.Duration(*waitSec) * time.Second)
			if !c.IsConnected() {
				fail("attacker lost its session")
			}
			push(c, []byte("attacker-alive"))
			want(echoFor([]byte("attacker-alive")))
			fmt.Println("RESULT PASS")
			c.Disconnect(100)
			return
		}
		deadline := time.Now().Add(time.Duration(*waitSec) * time.Second)
		for time.Now().Before(deadline) {
			if !c.IsConnected() {
				fmt.Println("EVENT DISCONNECTED")
				fmt.Println("RESULT PASS")
				c.Disconnect(100)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		fail("no disconnect within %ds", *waitSec)
	case "keepalive":
		if !connect() {
			fail("connect rejected")
		}
		time.Sleep(6 * time.Second)
		if !c.IsConnected() {
			fail("connection reaped despite heartbeat")
		}
		push(c, []byte("after-pings"))
		want(echoFor([]byte("after-pings")))
		fmt.Println("EVENT KEEPALIVE_OK")
		finish(c)
	case "reconnect":
		if !connect() {
			fail("connect rejected")
		}
		push(c, []byte("before"))
		want(echoFor([]byte("before")))
		fmt.Println("EVENT READY_FOR_KILL")
		os.Stdout.Sync()
		select {
		case <-lost:
		case <-time.After(30 * time.Second):
			fail("never observed connection loss")
		}
		fmt.Println("EVENT LOST_OBSERVED")
		os.Stdout.Sync()
		deadline := time.Now().Add(30 * time.Second)
		ok := false
		for time.Now().Before(deadline) && !ok {
			if c.IsConnected() {
				t := c.Push([]byte("after"))
				if t.WaitTimeout(2*time.Second) && t.Error() == nil {
					if matchEcho(echoFor([]byte("after")), 2*time.Second) {
						ok = true
						break
					}
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
		if !ok {
			fail("did not reconnect / echo after reconnect")
		}
		fmt.Println("EVENT RECONNECTED")
		finish(c)
	case "authreject":
		t := c.Connect()
		if t.WaitTimeout(5*time.Second) && t.Error() != nil {
			fmt.Printf("EVENT CONNECT_REJECTED %v\n", t.Error())
			fmt.Println("RESULT PASS")
			return
		}
		fail("connection was not rejected")
	default:
		fail("unknown scenario %s", *scenario)
	}
}

func push(c client.Client, payload []byte) {
	t := c.Push(payload)
	if !t.WaitTimeout(10*time.Second) {
		fail("push timed out")
	}
	if t.Error() != nil {
		fail("push: %v", t.Error())
	}
	fmt.Printf("EVENT SENT len=%d\n", len(payload))
}

func echoFor(p []byte) []byte {
	return append([]byte("echo:"), p...)
}

func want(payload []byte) {
	if !matchEcho(payload, 6*time.Second) {
		fail("timeout waiting for echo len=%d", len(payload))
	}
}

func matchEcho(payload []byte, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		select {
		case p := <-echoes:
			if bytes.Equal(p, payload) {
				fmt.Printf("EVENT ECHO_OK len=%d\n", len(p))
				return true
			}
			fmt.Printf("EVENT ECHO_OTHER len=%d payload=%q\n", len(p), truncate(p, 60))
		case <-deadline:
			return false
		}
	}
}

func finish(c client.Client) {
	c.Disconnect(100)
	fmt.Println("RESULT PASS")
	time.Sleep(100 * time.Millisecond)
}

func fail(format string, args ...interface{}) {
	fmt.Printf("RESULT FAIL "+format+"\n", args...)
	os.Exit(1)
}

func truncate(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}
