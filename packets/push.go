package packets

import (
	"bytes"
	"fmt"
	"io"
)

type PushPacket struct {
	FixedHeader
	Reserved byte
	Payload  []byte
}

func (p *PushPacket) Write(w io.Writer) error {
	var body bytes.Buffer
	var err error

	body.WriteByte(byte(0x00))

	p.FixedHeader.RemainingLength = body.Len() + len(p.Payload)
	packet := p.FixedHeader.pack()
	packet.Write(body.Bytes())
	packet.Write(p.Payload)
	_, err = w.Write(packet.Bytes())

	return err
}
func (p *PushPacket) Unpack(b io.Reader) error {

	var err error
	p.Reserved, err = decodeByte(b)
	if err != nil {
		return err
	}
	var payloadLength = p.FixedHeader.RemainingLength - 1
	if payloadLength < 0 {
		return fmt.Errorf("error unpacking, payload length < 0")
	}
	p.Payload = make([]byte, payloadLength)
	_, err = b.Read(p.Payload)

	return err
}
