package server

type Authenticator interface {
	Authenticate(credential string) (deviceID string, allowed bool)
}

type MessageHandler func(deviceID string, payload []byte)

type ConnectionListener interface {
	OnConnectionEstablished(deviceID string)
	OnConnectionClosed(deviceID string, reason string)
}

type noopConnectionListener struct{}

func (n noopConnectionListener) OnConnectionEstablished(deviceID string) {}
func (n noopConnectionListener) OnConnectionClosed(deviceID string, reason string) {
}
