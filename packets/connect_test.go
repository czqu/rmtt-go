package packets

import (
	"bytes"
	"testing"
)

func TestConnectPacket_Write(t *testing.T) {
	token := "egwegvregbershb"
	cp := &ConnectPacket{
		FixedHeader: FixedHeader{
			MessageType: Connect,
		},
		MagicNumber:     0x12345678,
		ProtocolVersion: 0x01,
		Reserved:        0x00,
		Keepalive:       60,
		Token:           token,
	}

	var buf bytes.Buffer
	err := cp.Write(&buf)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	expected := append([]byte{Connect << 4}, encodeLength(4+1+1+2+2+len(token))...)
	expected = append(expected, encodeUint32(0x12345678)...)
	expected = append(expected, 0x01, 0x00)
	expected = append(expected, encodeUint16(60)...)
	expected = append(expected, encodeString(token)...)

	if !bytes.Equal(buf.Bytes(), expected) {
		t.Errorf("Write() = %v,\n want %v", buf.Bytes(), expected)
	}
}

func TestConnectPacket_Unpack(t *testing.T) {
	data := append(encodeUint32(0x12345678), 0x01, 0x00)
	data = append(data, encodeUint16(60)...)
	data = append(data, encodeString("client123")...)
	buf := bytes.NewReader(data)

	cp := &ConnectPacket{
		FixedHeader: FixedHeader{
			RemainingLength: len(data),
		},
	}
	err := cp.Unpack(buf)
	if err != nil {
		t.Fatalf("Unpack() error = %v", err)
	}

	if cp.MagicNumber != 0x12345678 {
		t.Errorf("Unpack() MagicNumber = %v, want %v", cp.MagicNumber, 0x12345678)
	}
	if cp.ProtocolVersion != 0x01 {
		t.Errorf("Unpack() ProtocolVersion = %v, want %v", cp.ProtocolVersion, 0x01)
	}
	if cp.Reserved != 0x00 {
		t.Errorf("Unpack() Reserved = %v, want %v", cp.Reserved, 0x00)
	}
	if cp.Keepalive != 60 {
		t.Errorf("Unpack() Keepalive = %v, want %v", cp.Keepalive, 60)
	}
	if cp.Token != "client123" {
		t.Errorf("Unpack() Token = %v, want %v", cp.Token, "client123")
	}
}
