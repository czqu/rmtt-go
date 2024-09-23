package packets

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestEncodeLength(t *testing.T) {
	tests := []struct {
		length   int
		expected []byte
	}{
		{0, []byte{0}},
		{127, []byte{127}},
		{128, []byte{128, 1}},
		{16383, []byte{255, 127}},
		{16384, []byte{128, 128, 1}},
	}

	for _, tt := range tests {
		result := encodeLength(tt.length)
		if !bytes.Equal(result, tt.expected) {
			t.Errorf("encodeLength(%d) = %v, want %v", tt.length, result, tt.expected)
		}
	}
}

func TestDecodeLength(t *testing.T) {
	tests := []struct {
		data     []byte
		expected int
	}{
		{[]byte{0}, 0},
		{[]byte{127}, 127},
		{[]byte{128, 1}, 128},
		{[]byte{255, 127}, 16383},
		{[]byte{128, 128, 1}, 16384},
	}

	for _, tt := range tests {
		buf := bytes.NewReader(tt.data)
		result, err := decodeLength(buf)
		if err != nil {
			t.Fatalf("decodeLength() error = %v", err)
		}
		if result != tt.expected {
			t.Errorf("decodeLength(%v) = %d, want %d", tt.data, result, tt.expected)
		}
	}
}

func TestReadPacket(t *testing.T) {
	data := []byte{Connack << 4, 1, Accepted}
	buf := bytes.NewReader(data)

	packet, err := ReadPacket(buf)
	if err != nil {
		t.Fatalf("ReadPacket() error = %v", err)
	}

	connackPacket, ok := packet.(*ConnackPacket)
	if !ok {
		t.Fatalf("ReadPacket() = %T, want *ConnackPacket", packet)
	}

	if connackPacket.ReturnCode != Accepted {
		t.Errorf("ReadPacket() ReturnCode = %v, want %v", connackPacket.ReturnCode, Accepted)
	}
}

func TestReadPacket_Error(t *testing.T) {
	data := []byte{Connack << 4, 2}
	buf := bytes.NewReader(data)

	_, err := ReadPacket(buf)
	if err == nil {
		t.Fatalf("ReadPacket() error = nil, want error")
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("ReadPacket() error = %v, want %v", err, io.EOF)
	}
}
func TestFixedHeader_Pack(t *testing.T) {
	fh := FixedHeader{
		MessageType:     Connack,
		RemainingLength: 2,
	}

	packet := fh.pack()
	expected := []byte{Connack << 4, 2}
	if !bytes.Equal(packet.Bytes(), expected) {
		t.Errorf("pack() = %v, want %v", packet.Bytes(), expected)
	}
}

func TestFixedHeader_Unpack(t *testing.T) {

	data := []byte{Connack << 4, 1}
	buf := bytes.NewReader(data)

	tpe, err := buf.ReadByte()
	if err != nil {
		t.Fatalf("ReadPacket() error = %v", err)
	}
	var fh FixedHeader
	err = fh.unpack(tpe, buf)
	if err != nil {
		t.Fatalf("unpack() error = %v", err)
	}

	if fh.MessageType != Connack {
		t.Errorf("unpack() MessageType = %v, want %v", fh.MessageType, Connack)
	}
	if fh.RemainingLength != 1 {
		t.Errorf("unpack() RemainingLength = %v, want %v", fh.RemainingLength, 1)
	}
}
func TestDecodeByte(t *testing.T) {
	tests := []struct {
		data     []byte
		expected byte
	}{
		{[]byte{0x00}, 0x00},
		{[]byte{0xFF}, 0xFF},
	}

	for _, tt := range tests {
		buf := bytes.NewReader(tt.data)
		result, err := decodeByte(buf)
		if err != nil {
			t.Fatalf("decodeByte() error = %v", err)
		}
		if result != tt.expected {
			t.Errorf("decodeByte(%v) = %v, want %v", tt.data, result, tt.expected)
		}
	}
}

func TestEncodeUint32(t *testing.T) {
	tests := []struct {
		num      uint32
		expected []byte
	}{
		{0x00000000, []byte{0x00, 0x00, 0x00, 0x00}},
		{0xFFFFFFFF, []byte{0xFF, 0xFF, 0xFF, 0xFF}},
		{0x12345678, []byte{0x12, 0x34, 0x56, 0x78}},
	}

	for _, tt := range tests {
		result := encodeUint32(tt.num)
		if !bytes.Equal(result, tt.expected) {
			t.Errorf("encodeUint32(%v) = %v, want %v", tt.num, result, tt.expected)
		}
	}
}

func TestDecodeUint32(t *testing.T) {
	tests := []struct {
		data     []byte
		expected uint32
	}{
		{[]byte{0x00, 0x00, 0x00, 0x00}, 0x00000000},
		{[]byte{0xFF, 0xFF, 0xFF, 0xFF}, 0xFFFFFFFF},
		{[]byte{0x12, 0x34, 0x56, 0x78}, 0x12345678},
	}

	for _, tt := range tests {
		buf := bytes.NewReader(tt.data)
		result, err := decodeUint32(buf)
		if err != nil {
			t.Fatalf("decodeUint32() error = %v", err)
		}
		if result != tt.expected {
			t.Errorf("decodeUint32(%v) = %v, want %v", tt.data, result, tt.expected)
		}
	}
}

func TestEncodeUint16(t *testing.T) {
	tests := []struct {
		num      uint16
		expected []byte
	}{
		{0x0000, []byte{0x00, 0x00}},
		{0xFFFF, []byte{0xFF, 0xFF}},
		{0x1234, []byte{0x12, 0x34}},
	}

	for _, tt := range tests {
		result := encodeUint16(tt.num)
		if !bytes.Equal(result, tt.expected) {
			t.Errorf("encodeUint16(%v) = %v, want %v", tt.num, result, tt.expected)
		}
	}
}

func TestDecodeUint16(t *testing.T) {
	tests := []struct {
		data     []byte
		expected uint16
	}{
		{[]byte{0x00, 0x00}, 0x0000},
		{[]byte{0xFF, 0xFF}, 0xFFFF},
		{[]byte{0x12, 0x34}, 0x1234},
	}

	for _, tt := range tests {
		buf := bytes.NewReader(tt.data)
		result, err := decodeUint16(buf)
		if err != nil {
			t.Fatalf("decodeUint16() error = %v", err)
		}
		if result != tt.expected {
			t.Errorf("decodeUint16(%v) = %v, want %v", tt.data, result, tt.expected)
		}
	}
}

func TestDecodeBytes(t *testing.T) {
	tests := []struct {
		data     []byte
		expected []byte
		err      error
	}{
		{[]byte{0x00, 0x00}, nil, io.EOF},
		{[]byte{0x00, 0x03, 0x01, 0x02, 0x03}, []byte{0x01, 0x02, 0x03}, nil},
	}

	for _, tt := range tests {
		buf := bytes.NewReader(tt.data)
		result, err := decodeBytes(buf)
		if !errors.Is(err, tt.err) {
			t.Fatalf("decodeBytes() error = %v, want %v", err, tt.err)
		}
		if err == nil && !bytes.Equal(result, tt.expected) {
			t.Errorf("decodeBytes(%v) = %v, want %v", tt.data, result, tt.expected)
		}
	}
}
func TestEncodeBytes(t *testing.T) {
	tests := []struct {
		field    []byte
		expected []byte
	}{
		{[]byte{}, []byte{0x00, 0x00}},
		{[]byte{0x01, 0x02, 0x03}, []byte{0x00, 0x03, 0x01, 0x02, 0x03}},
	}

	for _, tt := range tests {
		result := encodeBytes(tt.field)
		if !bytes.Equal(result, tt.expected) {
			t.Errorf("encodeBytes(%v) = %v, want %v", tt.field, result, tt.expected)
		}
	}
}

func TestEncodeString(t *testing.T) {
	tests := []struct {
		field    string
		expected []byte
	}{
		{"", []byte{0x00, 0x00}},
		{"abc", []byte{0x00, 0x03, 'a', 'b', 'c'}},
	}

	for _, tt := range tests {
		result := encodeString(tt.field)
		if !bytes.Equal(result, tt.expected) {
			t.Errorf("encodeString(%v) = %v, want %v", tt.field, result, tt.expected)
		}
	}
}

func TestDecodeString(t *testing.T) {
	tests := []struct {
		data     []byte
		expected string
		err      error
	}{
		{[]byte{0x00}, "", io.EOF},
		{[]byte{0x00, 0x03, 'a', 'b', 'c'}, "abc", nil},
	}

	for _, tt := range tests {
		buf := bytes.NewReader(tt.data)
		result, err := decodeString(buf)
		if !errors.Is(err, tt.err) {
			t.Fatalf("decodeString() error = %v, want %v", err, tt.err)
		}
		if result != tt.expected {
			t.Errorf("decodeString(%v) = %v, want %v", tt.data, result, tt.expected)
		}
	}
}
