# rmtt-go — rmtt protocol Go implementation

A Go client and server library for the Remote Message Telemetry Transport (rmtt) protocol. The API design borrows from [paho.mqtt.golang](https://github.com/eclipse/paho.mqtt.golang), so developers familiar with MQTT can get started quickly.

Supports **six transports**: `tcp://` `kcp://` `tls://` `quic://` `ws://` `wss://`, interoperable between client and server.

## Installation

```bash
go get -u github.com/czqu/rmtt-go
```

## Client

### Minimal example

```go
package main

import (
	"log"
	"time"

	"github.com/czqu/rmtt-go/client"
)

func main() {
	opts := client.NewClientOptions()
	opts.AddServer("tcp://127.0.0.1:18883")
	opts.SetCredential("dev-001")
	opts.SetConnectTimeout(5 * time.Second)
	opts.AutoReconnect = true

	c := client.NewClient(opts)
	c.AddPayloadHandlerLast(func(cl client.Client, msg client.Message) {
		log.Printf("received: %s", string(msg.Payload()))
	})

	if token := c.Connect(); token.Wait() && token.Error() != nil {
		log.Fatalf("connect failed: %v", token.Error())
	}

	token := c.Push("hello")
	token.Wait()

	c.Disconnect(1000)
}
```

### Choosing a transport

`AddServer` selects the transport from the URL scheme; bare addresses without a scheme are treated as `tcp://`:

```go
opts.AddServer("tcp://127.0.0.1:18883")    // TCP
opts.AddServer("kcp://127.0.0.1:18883")    // KCP over UDP
opts.AddServer("tls://127.0.0.1:18884")    // TLS over TCP, needs SetTlsConfig
opts.AddServer("quic://127.0.0.1:18885")   // QUIC (TLS 1.3 built in)
opts.AddServer("ws://127.0.0.1:18886")     // WebSocket
opts.AddServer("wss://127.0.0.1:18887")    // WSS, needs SetTlsConfig
```

TLS-based transports require a certificate configuration:

```go
opts.SetTlsConfig(&tls.Config{InsecureSkipVerify: true}) // test environments only
```

QUIC ships with hardened transport defaults (`MaxIdleTimeout` 15min, `KeepAlivePeriod` 30s) so an otherwise idle connection is never torn down while application heartbeats grow longer. Override them with `SetQuicConfig` (a config with `KeepAlivePeriod <= 0` is rejected and falls back to the default; `nil` restores the default):

```go
opts.SetQuicConfig(&quic.Config{MaxIdleTimeout: 30 * time.Minute, KeepAlivePeriod: 30 * time.Second})
```

### Key options

| Option | Default | Description |
|--------|---------|-------------|
| `SetCredential(id)` | — | Credential carried in CONNECT; the server authenticates and derives the device identity from it |
| `SetHeartbeat(d)` | 10s | Heartbeat interval (the server may push a suggested value back in CONNACK) |
| `SetConnectTimeout(d)` | 30s | Connection handshake timeout |
| `SetWriteTimeout(d)` | 30s | Write timeout |
| `AutoReconnect` | true | Auto-reconnect after a lost connection (exponential backoff + jitter) |
| `ConnectRetry` | true | Retry on initial connect failure |
| `SetReconnectBase(d)` | 1s | Reconnect backoff base |
| `SetReconnectJitter(j)` | 0.25 | Reconnect backoff jitter |

### Adaptive heartbeat

A fixed heartbeat cannot serve both low traffic and low latency. When enabled, the client probes with a short period first, then doubles and fine-tunes until it finds the maximum sustainable interval for the current network, settling at ~90% of it; the ceiling is bounded by both `maxSeconds` and the serverKp negotiated in CONNACK:

```go
opts.SetAdaptiveHeartbeat(10, 300)               // short=10s, max=300s
opts.SetProbeCount(3)                            // consecutive successful short heartbeats before probing starts
opts.SetResponseWindow(2 * time.Second)          // PINGRESP wait window
opts.SetFineStep(5)                              // fine-tuning step (seconds)
```

A lost heartbeat in the stable state falls back to the short period and re-adapts. Mutually exclusive with `SetHeartbeat` (adaptive wins).

### Sending and awaiting results

`Push` accepts `string`, `[]byte` or `bytes.Buffer` as payload; results are confirmed asynchronously through the returned `Token`:

```go
token := c.Push("hello")
if token.WaitTimeout(2 * time.Second) && token.Error() != nil {
	log.Printf("push failed: %v", token.Error())
}
// or client.WaitTokenTimeout(token, d), which returns client.TimedOut on timeout
```

### Callbacks

```go
c.AddPayloadHandlerLast(func(cl client.Client, msg client.Message) { /* PUSH received */ })
opts.OnConnectionLost = func(c client.Client, err error) { /* connection lost */ }
opts.OnReconnecting = func(c client.Client, o *client.ClientOptions) { /* reconnecting */ }
opts.OnConnectAttempt = func(server *url.URL, tlsCfg *tls.Config) *tls.Config { /* per connection attempt */ }
```

### Logging

Both the client and server libraries provide a paho-style pluggable `Logger` (the interface is just `Println/Printf` of `log.Logger`, so `*log.Logger` works directly). The package-level variables default to `NOOPLogger` (fully silent).

```go
client.SetLogger(log.New(os.Stdout, "", 0))        // one logger for all levels
client.SetDebugLogger(customLogger)                 // or set per level
// the server package offers the same: server.SetLogger / SetErrorLogger / SetWarnLogger / SetInfoLogger / SetDebugLogger
```

Level conventions: `DEBUG`=heartbeat send/receive and adaptive transitions; `INFO`=CONNACK serverKp/fixed heartbeat config/adaptive parameters/device connections; `WARN`=reconnects/keepalive timeouts/adaptive lost heartbeats; `ERROR`=handshake failures and anomalies. Log lines look like `[client]   ...` / `[net]     ...`. To suppress DEBUG, use a logger that only outputs selected levels (e.g. filtered by the `[INFO]` prefix).

## Server

### Minimal example

```go
package main

import (
	"log"

	"github.com/czqu/rmtt-go/server"
)

type auth struct{}

func (a *auth) Authenticate(credential string) (string, bool) {
	if len(credential) > 0 {
		return credential, true // credential is the device ID
	}
	return "", false
}

func main() {
	opts := server.NewServerOptions()
	opts.SetPort(18883)
	opts.SetAuthenticator(&auth{})
	opts.SetMessageHandler(func(deviceID string, payload []byte) {
		log.Printf("device %s sent: %s", deviceID, string(payload))
	})
	opts.SetConnectionListener(&connListener{})

	srv := server.NewServer(opts)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
```

### Listening on multiple transports

Register additional transports with `AddListener`; a single process can listen on several transports at once (TCP and KCP port spaces are independent):

```go
opts := server.NewServerOptions()
opts.AddListener(server.NewTCPListener(":18883"))
opts.AddListener(server.NewKCPListener(":18883"))       // UDP, may share the port with TCP
opts.AddListener(server.NewTLSListener(":18884", tlsCfg))
opts.AddListener(server.NewQUICListener(":18885", tlsCfg))
opts.AddListener(server.NewWSListener(":18886", "/ws"))
opts.AddListener(server.NewWSSListener(":18887", "/ws", tlsCfg))
```

> Without `AddListener`, the server listens on a single TCP socket on the port set with `SetPort`.

### Server interface

```go
type Server interface {
	ListenAndServe() error
	ListenAndServeContext(ctx context.Context) error
	Push(deviceID string, payload []byte) error // downlink push
	Kick(deviceID string, reason byte) error    // force disconnect
	Close() error
}
```

### Extension points

| Interface | Responsibility |
|-----------|----------------|
| `Authenticator` | Authentication: `Authenticate(credential) (deviceID, ok)`, injected by the application |
| `MessageHandler` | Handles device uplink PUSH |
| `ConnectionListener` | Connection established/closed event callbacks |
| `KeepalivePolicy` | Heartbeat negotiation policy (defaults from `DefaultKeepalivePolicy()`) |

### DISCONNECT reason codes

`Kick(deviceID, reason)` and server-initiated disconnects use the following reason codes:

| Code | Meaning |
|------|---------|
| `0x00` | Normal disconnect |
| `0x01` | Credential expired |
| `0x02` | Session taken over (same device connected twice) |
| `0x03` | Server shutdown |
| `0x04` | Protocol violation |
| `0x05` | Keepalive timeout |
| `0x06` | Kicked by admin |
| `0x07` | Rate limited |
| `0x08` | Credential rejected |
| `0xFE` | Unknown error |

## Directory layout

```
client/   client implementation (connection, heartbeat, reconnect, Token)
server/   server implementation (connection management, routing, six-transport Listener, Keepalive)
codec/    rmtt protocol codec (CONNECT/CONNACK/PUSH/PINGREQ/PINGRESP/DISCONNECT)
cmd/      runnable minimal client / server examples
```

## Tests

```bash
go test ./...
```
