package client

import "github.com/czqu/rmtt-go/codec"

type Message interface {
	Payload() []byte
}

type message struct {
	payload []byte
}

func (m *message) Payload() []byte {
	return m.payload
}

func messageFromPush(p *codec.PushPacket, ack func()) Message {
	return &message{
		payload: p.Payload,
	}
}
