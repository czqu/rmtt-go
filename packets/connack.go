package packets

import (
	"bytes"
	"io"
)

type ConnackPacket struct {
	FixedHeader
	ReturnCode byte
}

func (ca *ConnackPacket) Write(w io.Writer) error {
	var body bytes.Buffer
	var err error

	body.WriteByte(ca.ReturnCode)
	ca.FixedHeader.RemainingLength = 2
	packet := ca.FixedHeader.pack()
	packet.Write(body.Bytes())
	_, err = packet.WriteTo(w)

	return err
}

func (ca *ConnackPacket) Unpack(b io.Reader) error {
	var err error
	ca.ReturnCode, err = decodeByte(b)
	return err
}
