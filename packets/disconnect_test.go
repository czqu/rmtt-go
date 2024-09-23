package packets

import (
	"bytes"
	"testing"
)

func TestDisconnectPacket_Write(t *testing.T) {
	dp := &DisconnectPacket{
		FixedHeader: FixedHeader{
			MessageType: Disconnect,
		},
		returnCode: 0x01,
	}

	var buf bytes.Buffer
	err := dp.Write(&buf)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	expected := []byte{Disconnect << 4, 0}
	if !bytes.Equal(buf.Bytes(), expected) {
		t.Errorf("Write() = %v, want %v", buf.Bytes(), expected)
	}
}

func TestDisconnectPacket_Unpack(t *testing.T) {
	data := []byte{0x01}
	buf := bytes.NewReader(data)

	dp := &DisconnectPacket{}
	err := dp.Unpack(buf)
	if err != nil {
		t.Fatalf("Unpack() error = %v", err)
	}

	if dp.returnCode != 0x01 {
		t.Errorf("Unpack() returnCode = %v, want %v", dp.returnCode, 0x01)
	}
}

func TestDisconnectPacket_GetReturnCode(t *testing.T) {
	dp := &DisconnectPacket{
		returnCode: 0x01,
	}

	if dp.GetReturnCode() != 0x01 {
		t.Errorf("GetReturnCode() = %v, want %v", dp.GetReturnCode(), 0x01)
	}
}
