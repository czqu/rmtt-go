package server

type ServerOptions struct {
	Port               int
	Authenticator      Authenticator
	MessageHandler     MessageHandler
	ConnectionListener ConnectionListener
	KeepalivePolicy    *KeepalivePolicy

	listeners []Listener
}

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

func (o *ServerOptions) SetPort(port int) *ServerOptions {
	o.Port = port
	return o
}

func (o *ServerOptions) SetAuthenticator(auth Authenticator) *ServerOptions {
	o.Authenticator = auth
	return o
}

func (o *ServerOptions) SetMessageHandler(handler MessageHandler) *ServerOptions {
	o.MessageHandler = handler
	return o
}

func (o *ServerOptions) SetConnectionListener(listener ConnectionListener) *ServerOptions {
	o.ConnectionListener = listener
	return o
}

func (o *ServerOptions) SetKeepalivePolicy(policy *KeepalivePolicy) *ServerOptions {
	o.KeepalivePolicy = policy
	return o
}
