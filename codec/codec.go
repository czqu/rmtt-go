package codec

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ControlPacket is a decoded rmtt packet that can encode itself to a writer
// and decode its variable header plus payload from a reader.
type ControlPacket interface {
	Write(io.Writer) error
	Unpack(io.Reader) error
}

// FixedHeader is the frame header: the message type byte and the encoded
// remaining length of the variable header plus payload.
type FixedHeader struct {
	MessageType     byte
	Flags           byte
	RemainingLength int
}

// ReadPacket reads a single full control packet from r.
func ReadPacket(r io.Reader) (ControlPacket, error) {
	var fh FixedHeader
	b := make([]byte, 1)

	_, err := io.ReadFull(r, b)
	if err != nil {
		return nil, err
	}

	err = fh.unpack(b[0], r)
	if err != nil {
		if errors.Is(err, ErrInvalidFixedHeaderFlags) {
			discardFrameBody(r, fh)
		}
		return nil, err
	}

	// Reject the claimed size before allocating: the peer controls this
	// length and has not been authenticated yet. A length of 0 is legal
	// (PINGREQ/PINGRESP carry no body).
	if fh.RemainingLength < 0 || fh.RemainingLength > MaxPacketSize {
		return nil, fmt.Errorf("%w: %d (max %d)", ErrPacketTooLarge, fh.RemainingLength, MaxPacketSize)
	}

	cp, err := NewControlPacketWithHeader(fh)
	if err != nil {
		discardFrameBody(r, fh)
		return nil, err
	}

	packetBytes := make([]byte, fh.RemainingLength)
	n, err := io.ReadFull(r, packetBytes)
	if err != nil {
		return nil, err
	}
	if n != fh.RemainingLength {
		return nil, errors.New("failed to read expected data")
	}

	err = cp.Unpack(bytes.NewBuffer(packetBytes))
	return cp, err
}

func (fh *FixedHeader) pack() bytes.Buffer {
	var header bytes.Buffer
	header.WriteByte(fh.MessageType<<4 | byte(0x00))
	header.Write(encodeLength(fh.RemainingLength))
	return header
}

func encodeLength(length int) []byte {
	var encLength []byte
	for {
		digit := byte(length % 128)
		length /= 128
		if length > 0 {
			digit |= 0x80
		}
		encLength = append(encLength, digit)
		if length == 0 {
			break
		}
	}
	return encLength
}

func (fh *FixedHeader) unpack(typeAndFlags byte, r io.Reader) error {
	fh.MessageType = typeAndFlags >> 4
	fh.Flags = typeAndFlags & 0x0F

	var err error
	fh.RemainingLength, err = decodeLength(r)
	if err != nil {
		return err
	}
	if fh.Flags != 0x00 {
		return fmt.Errorf("%w: 0x%x", ErrInvalidFixedHeaderFlags, fh.Flags)
	}
	return nil
}

// discardFrameBody consumes the body of a rejected frame when it is safely
// bounded by MaxPacketSize. Consuming it keeps the byte stream in sync and
// stops close() from turning into a TCP reset: unread bytes make the kernel
// reset the connection, discarding any DISCONNECT(0x04) protocol-violation
// reply the caller writes before closing.
func discardFrameBody(r io.Reader, fh FixedHeader) {
	if fh.RemainingLength >= 0 && fh.RemainingLength <= MaxPacketSize {
		_, _ = io.CopyN(io.Discard, r, int64(fh.RemainingLength))
	}
}

func decodeLength(r io.Reader) (int, error) {
	var rLength uint32
	var multiplier uint32
	b := make([]byte, 1)
	for {
		if _, err := io.ReadFull(r, b); err != nil {
			return 0, err
		}

		digit := b[0]
		rLength |= uint32(digit&127) << multiplier
		if (digit & 128) == 0 {
			return int(rLength), nil
		}
		multiplier += 7
		// At most four bytes may carry the length; a continuation bit past
		// that is a malformed varint, not a silently truncated value.
		if multiplier >= 28 {
			return 0, ErrMalformedRemainingLength
		}
	}
}

// newPacket returns an empty packet carrying fh, or nil for unknown types.
// It is the single type dispatch shared by the packet factories below.
func newPacket(fh FixedHeader) ControlPacket {
	switch fh.MessageType {
	case Connect:
		return &ConnectPacket{FixedHeader: fh}
	case Connack:
		return &ConnackPacket{FixedHeader: fh}
	case Disconnect:
		return &DisconnectPacket{FixedHeader: fh}
	case Push:
		return &PushPacket{FixedHeader: fh}
	case Pingreq:
		return &PingreqPacket{FixedHeader: fh}
	case Pingresp:
		return &PingrespPacket{FixedHeader: fh}
	}
	return nil
}

// NewControlPacket returns an empty packet of the given type, suitable for
// encoding; unknown types yield nil.
func NewControlPacket(packetType byte) ControlPacket {
	return newPacket(FixedHeader{MessageType: packetType})
}

// NewControlPacketWithHeader returns an empty packet of the type carried by
// fh; unknown types yield an ErrUnsupportedPacketType error.
func NewControlPacketWithHeader(fh FixedHeader) (ControlPacket, error) {
	cp := newPacket(fh)
	if cp == nil {
		return nil, fmt.Errorf("%w 0x%x", ErrUnsupportedPacketType, fh.MessageType)
	}
	return cp, nil
}

func decodeByte(b io.Reader) (byte, error) {
	num := make([]byte, 1)
	if _, err := io.ReadFull(b, num); err != nil {
		return 0, err
	}
	return num[0], nil
}

func encodeUint32(num uint32) []byte {
	bytesResult := make([]byte, 4)
	binary.BigEndian.PutUint32(bytesResult, num)
	return bytesResult
}

func decodeUint32(b io.Reader) (uint32, error) {
	num := make([]byte, 4)
	if _, err := io.ReadFull(b, num); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(num), nil
}

func encodeUint16(num uint16) []byte {
	bytesResult := make([]byte, 2)
	binary.BigEndian.PutUint16(bytesResult, num)
	return bytesResult
}

func decodeUint16(b io.Reader) (uint16, error) {
	num := make([]byte, 2)
	if _, err := io.ReadFull(b, num); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(num), nil
}

func decodeBytes(b io.Reader) ([]byte, error) {
	fieldLength, err := decodeUint16(b)
	if err != nil {
		return nil, err
	}

	field := make([]byte, fieldLength)
	if _, err := io.ReadFull(b, field); err != nil {
		return nil, err
	}
	return field, nil
}

func encodeBytes(field []byte) []byte {
	fieldLength := make([]byte, 2)
	binary.BigEndian.PutUint16(fieldLength, uint16(len(field)))
	return append(fieldLength, field...)
}

func encodeString(field string) []byte {
	return encodeBytes([]byte(field))
}

func decodeString(b io.Reader) (string, error) {
	buf, err := decodeBytes(b)
	return string(buf), err
}
