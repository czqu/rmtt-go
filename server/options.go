package server

import "time"

// ServerOptions holds the configuration for a Server. Create with
// NewServerOptions and adjust with the Set* helpers or direct field
// assignment.
type ServerOptions struct {
	Port               int
	Authenticator      Authenticator
	MessageHandler     MessageHandler
	ConnectionListener ConnectionListener
	KeepalivePolicy    *KeepalivePolicy
	// HandshakeTimeout bounds how long an accepted connection may take to
	// deliver and authenticate its CONNECT packet. Without it a peer can hold
	// a connection open indefinitely without sending anything.
	HandshakeTimeout time.Duration

	listeners []Listener
}

// NewServerOptions returns ServerOptions with library defaults: TCP port
// 18883, DefaultKeepalivePolicy and a 10s handshake timeout.
func NewServerOptions() *ServerOptions {
	return &ServerOptions{
		Port:             18883,
		KeepalivePolicy:  DefaultKeepalivePolicy(),
		HandshakeTimeout: 10 * time.Second,
	}
}

// AddListener registers an extra transport listener (KCP, TLS, QUIC, WS, WSS...).
// If no listener is added, a single TCP listener on Port is used.
func (o *ServerOptions) AddListener(l Listener) *ServerOptions {
	o.listeners = append(o.listeners, l)
	return o
}

// SetPort sets the port for the default single TCP listener, used only when
// no listener was added via AddListener.
func (o *ServerOptions) SetPort(port int) *ServerOptions {
	o.Port = port
	return o
}

// SetAuthenticator installs the authentication callback. When nil, the
// CONNECT credential is used directly as the device ID.
func (o *ServerOptions) SetAuthenticator(auth Authenticator) *ServerOptions {
	o.Authenticator = auth
	return o
}

// SetMessageHandler installs the callback invoked for each uplink PUSH.
func (o *ServerOptions) SetMessageHandler(handler MessageHandler) *ServerOptions {
	o.MessageHandler = handler
	return o
}

// SetConnectionListener installs the connection lifecycle event callbacks.
func (o *ServerOptions) SetConnectionListener(listener ConnectionListener) *ServerOptions {
	o.ConnectionListener = listener
	return o
}

// SetKeepalivePolicy replaces the keepalive negotiation policy.
func (o *ServerOptions) SetKeepalivePolicy(policy *KeepalivePolicy) *ServerOptions {
	o.KeepalivePolicy = policy
	return o
}

// SetHandshakeTimeout sets the deadline for the CONNECT handshake on newly
// accepted connections.
func (o *ServerOptions) SetHandshakeTimeout(d time.Duration) *ServerOptions {
	o.HandshakeTimeout = d
	return o
}
