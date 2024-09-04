package RMTT

import "github.com/czqu/rmtt-go/packets"

type Message interface {
	Payload() []byte
}
type message struct {
	payload []byte
}

func (m *message) Payload() []byte {
	return m.payload
}
func messageFromPush(p *packets.PushPacket, ack func()) Message {
	return &message{
		payload: p.Payload,
	}
}
