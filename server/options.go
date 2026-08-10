package server

// ServerOptions holds the configuration for a Server. Create with
// NewServerOptions and adjust with the Set* helpers or direct field
// assignment.
type ServerOptions struct {
	Port               int
	Authenticator      Authenticator
	MessageHandler     MessageHandler
	ConnectionListener ConnectionListener
	KeepalivePolicy    *KeepalivePolicy

	listeners []Listener
}

// NewServerOptions returns ServerOptions with library defaults: TCP port
// 18883 and DefaultKeepalivePolicy.
func NewServerOptions() *ServerOptions {
	return &ServerOptions{
		Port:            18883,
		KeepalivePolicy: DefaultKeepalivePolicy(),
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
