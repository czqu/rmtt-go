package packets

import "io"

type DisconnectPacket struct {
	FixedHeader
	returnCode byte
}

func (d *DisconnectPacket) Write(w io.Writer) error {
	packet := d.FixedHeader.pack()
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
