package packets

import "io"

type PingreqPacket struct {
	FixedHeader
}

func (pr *PingreqPacket) Write(w io.Writer) error {
	packet := pr.FixedHeader.pack()
	_, err := packet.WriteTo(w)

	return err
}

func (pr *PingreqPacket) Unpack(b io.Reader) error {
	return nil
}
