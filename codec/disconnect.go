package codec

import (
	"bytes"
	"io"
)

type DisconnectPacket struct {
	FixedHeader
	returnCode byte
}

func (d *DisconnectPacket) Write(w io.Writer) error {
	var body bytes.Buffer
	body.WriteByte(d.returnCode)
	d.FixedHeader.RemainingLength = 1
	packet := d.FixedHeader.pack()
	packet.Write(body.Bytes())
	_, err := packet.WriteTo(w)
	return err
}

func (d *DisconnectPacket) Unpack(r io.Reader) error {
	b := make([]byte, 1)
	_, err := io.ReadFull(r, b)
	if err != nil {
		return err
	}
	d.returnCode = b[0]

	return nil
}

func (d *DisconnectPacket) GetReturnCode() byte {
	return d.returnCode
}

func (d *DisconnectPacket) SetReturnCode(code byte) {
	d.returnCode = code
}
