// Package client implements the rmtt protocol client library.
//
// A client connects to a server over one of six transports (tcp, kcp, tls,
// quic, ws, wss), authenticates with a credential, exchanges PUSH messages
// and keeps the connection alive with a fixed or adaptive heartbeat.
//
// Usage follows the paho.mqtt.golang style: configure a ClientOptions with
// NewClientOptions and its setters, create the client with NewClient, then
// call Connect and wait on the returned Token. Asynchronous results are
// delivered through Token, and inbound PUSH messages are dispatched to
// handlers registered with AddPayloadHandlerLast. Lost connections are
// retried with exponential backoff and jitter when AutoReconnect is set.
//
// The package is silent by default; install loggers with SetLogger or the
// per-level setters (SetErrorLogger/SetInfoLogger/SetWarnLogger/SetDebugLogger).
package client
