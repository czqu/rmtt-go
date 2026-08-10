package codec

import (
	"bytes"
	"io"
)

type ConnackPacket struct {
	FixedHeader
	ReturnCode      byte
	ServerKeepalive uint16
}

func (ca *ConnackPacket) Write(w io.Writer) error {
	var body bytes.Buffer
	var err error

	body.WriteByte(ca.ReturnCode)
	body.Write(encodeUint16(ca.ServerKeepalive))
	ca.FixedHeader.RemainingLength = 3
	packet := ca.FixedHeader.pack()
	packet.Write(body.Bytes())
	_, err = packet.WriteTo(w)

	return err
}

func (ca *ConnackPacket) Unpack(b io.Reader) error {
	var err error
	ca.ReturnCode, err = decodeByte(b)
	if err != nil {
		return err
	}
	var sk uint16
	if sk, err = decodeUint16(b); err != nil {
		return err
	}
	ca.ServerKeepalive = sk
	return nil
}
