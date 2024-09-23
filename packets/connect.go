package packets

import (
	"bytes"
	"io"
)

type ConnectPacket struct {
	FixedHeader
	MagicNumber     uint32
	ProtocolVersion byte
	Reserved        byte
	Keepalive       uint16
	Token           string
}

func (c *ConnectPacket) Write(w io.Writer) error {
	var body bytes.Buffer
	var err error
	body.Write(encodeUint32(c.MagicNumber))
	body.WriteByte(c.ProtocolVersion)
	body.WriteByte(c.Reserved)
	body.Write(encodeUint16(c.Keepalive))
	body.Write(encodeString(c.Token))

	c.FixedHeader.RemainingLength = body.Len()
	packet := c.FixedHeader.pack()
	packet.Write(body.Bytes())
	_, err = packet.WriteTo(w)

	return err
}

func (c *ConnectPacket) Unpack(b io.Reader) error {
	var err error
	c.MagicNumber, err = decodeUint32(b)
	if err != nil {
		return err
	}

	c.ProtocolVersion, err = decodeByte(b)
	if err != nil {
		return err
	}

	c.Reserved, err = decodeByte(b)
	if err != nil {
		return err
	}
	c.Keepalive, err = decodeUint16(b)
	if err != nil {
		return err
	}
	c.Token, err = decodeString(b)
	if err != nil {
		return err
	}

	return nil
}
