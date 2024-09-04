package packets

import "io"

type PingrespPacket struct {
	FixedHeader
}

func (pr *PingrespPacket) Write(w io.Writer) error {
	packet := pr.FixedHeader.pack()
	_, err := packet.WriteTo(w)

	return err
}

func (pr *PingrespPacket) Unpack(b io.Reader) error {
	return nil
}
