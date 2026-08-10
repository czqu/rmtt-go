package server

// Authenticator authenticates a CONNECT credential and maps it to a device
// ID; allowed=false rejects the connection.
type Authenticator interface {
	Authenticate(credential string) (deviceID string, allowed bool)
}

// MessageHandler receives the uplink PUSH payloads of connected devices.
type MessageHandler func(deviceID string, payload []byte)

// ConnectionListener receives device connection lifecycle events.
type ConnectionListener interface {
	OnConnectionEstablished(deviceID string)
	OnConnectionClosed(deviceID string, reason string)
}

type noopConnectionListener struct{}

func (n noopConnectionListener) OnConnectionEstablished(deviceID string) {}
func (n noopConnectionListener) OnConnectionClosed(deviceID string, reason string) {
}
