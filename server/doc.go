// Package server implements the rmtt protocol server library.
//
// A server accepts device connections over one or more transports (tcp, kcp,
// tls, quic, ws, wss) through Listener implementations, authenticates
// devices with an Authenticator, routes uplink PUSH messages to a
// MessageHandler and pushes downlink messages with Server.Push. Keepalive is
// negotiated per connection through KeepalivePolicy, and connection lifecycle
// events are reported through ConnectionListener.
//
// Usage: build a ServerOptions, register listeners with AddListener and
// install callbacks, then create the server with NewServer and serve with
// ListenAndServe. When no listener is added, a single TCP listener on
// options.Port is used.
//
// The package is silent by default; install loggers with SetLogger or the
// per-level setters (SetErrorLogger/SetInfoLogger/SetWarnLogger/SetDebugLogger).
package server
