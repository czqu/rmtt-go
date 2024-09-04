package packets

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

type ControlPacket interface {
	Write(io.Writer) error
	Unpack(io.Reader) error
}

const (
	Connect    = 1
	Connack    = 2
	Push       = 3
	Puback     = 4
	Pingreq    = 5
	Pingresp   = 6
	Disconnect = 14
)

type FixedHeader struct {
	MessageType     byte
	RemainingLength int
}

const (
	Accepted                     = 0x00
	ErrRefusedNotAuthorised      = 0x01
	ErrRefusedBadProtocolVersion = 0x03
	ErrRefusedServerUnavailable  = 0x02
	ErrNetworkError              = 0xFE
	ErrProtocolViolation         = 0xFF
)

var ConnackReturnCodes = map[uint8]string{
	0:   "Connection Accepted",
	1:   "Connection Refused: Bad Protocol Version",
	2:   "Connection Refused: Server Unavailable",
	3:   "Connection Refused: Not Authorised",
	254: "Connection Error",
	255: "Connection Refused: Protocol Violation",
}
var (
	ErrorRefusedBadProtocolVersion = errors.New("unacceptable protocol version")
	ErrorRefusedServerUnavailable  = errors.New("server Unavailable")
	ErrorRefusedNotAuthorised      = errors.New("not Authorized")
	ErrorNetworkError              = errors.New("network Error")
	ErrorProtocolViolation         = errors.New("protocol Violation")
)
var ConnErrors = map[byte]error{
	Accepted:                     nil,
	ErrRefusedBadProtocolVersion: ErrorRefusedBadProtocolVersion,
	ErrRefusedServerUnavailable:  ErrorRefusedServerUnavailable,
	ErrRefusedNotAuthorised:      ErrorRefusedNotAuthorised,
	ErrNetworkError:              ErrorNetworkError,
	ErrProtocolViolation:         ErrorProtocolViolation,
}

func ReadPacket(r io.Reader) (ControlPacket, error) {
	var fh FixedHeader
	b := make([]byte, 1)

	_, err := io.ReadFull(r, b)
	if err != nil {
		return nil, err
	}

	err = fh.unpack(b[0], r)
	if err != nil {
		return nil, err
	}

	cp, err := NewControlPacketWithHeader(fh)
	if err != nil {
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

	var err error
	fh.RemainingLength, err = decodeLength(r)
	return err
}
func decodeLength(r io.Reader) (int, error) {
	var rLength uint32
	var multiplier uint32
	b := make([]byte, 1)
	for multiplier < 27 { // fix: Infinite '(digit & 128) == 1' will cause the dead loop
		_, err := io.ReadFull(r, b)
		if err != nil {
			return 0, err
		}

		digit := b[0]
		rLength |= uint32(digit&127) << multiplier
		if (digit & 128) == 0 {
			break
		}
		multiplier += 7
	}
	return int(rLength), nil
}
func NewControlPacket(packetType byte) ControlPacket {
	switch packetType {
	case Connect:
		return &ConnectPacket{FixedHeader: FixedHeader{MessageType: Connect}}
	case Connack:
		return &ConnackPacket{FixedHeader: FixedHeader{MessageType: Connack}}
	case Disconnect:
		return &DisconnectPacket{FixedHeader: FixedHeader{MessageType: Disconnect}}
	case Push:
		return &PushPacket{FixedHeader: FixedHeader{MessageType: Push}}
	case Pingreq:
		return &PingreqPacket{FixedHeader: FixedHeader{MessageType: Pingreq}}
	case Pingresp:
		return &PingrespPacket{FixedHeader: FixedHeader{MessageType: Pingresp}}
	}
	return nil
}
func NewControlPacketWithHeader(fh FixedHeader) (ControlPacket, error) {
	switch fh.MessageType {
	case Connect:
		return &ConnectPacket{FixedHeader: fh}, nil
	case Connack:
		return &ConnackPacket{FixedHeader: fh}, nil
	case Disconnect:
		return &DisconnectPacket{FixedHeader: fh}, nil
	case Push:
		return &PushPacket{FixedHeader: fh}, nil

	case Pingreq:
		return &PingreqPacket{FixedHeader: fh}, nil
	case Pingresp:
		return &PingrespPacket{FixedHeader: fh}, nil
	}
	return nil, fmt.Errorf("unsupported packet type 0x%x", fh.MessageType)
}

func decodeByte(b io.Reader) (byte, error) {
	num := make([]byte, 1)
	_, err := b.Read(num)
	if err != nil {
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
	_, err := b.Read(num)
	if err != nil {
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
	_, err := b.Read(num)
	if err != nil {
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
	_, err = b.Read(field)
	if err != nil {
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
