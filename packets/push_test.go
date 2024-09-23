package packets

import (
	"bytes"
	"testing"
)

func TestPushPacket_Write(t *testing.T) {
	pp := &PushPacket{
		FixedHeader: FixedHeader{
			MessageType: Push,
		},
		Payload: []byte{0x01, 0x02, 0x03},
	}

	var buf bytes.Buffer
	err := pp.Write(&buf)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	expected := []byte{Push << 4, 4, 0x00, 0x01, 0x02, 0x03}
	if !bytes.Equal(buf.Bytes(), expected) {
		t.Errorf("Write() = %v, want %v", buf.Bytes(), expected)
	}
}

func TestPushPacket_Unpack(t *testing.T) {
	data := []byte{0x00, 0x01, 0x02, 0x03}
	buf := bytes.NewReader(data)

	pp := &PushPacket{
		FixedHeader: FixedHeader{
			RemainingLength: len(data),
		},
	}
	err := pp.Unpack(buf)
	if err != nil {
		t.Fatalf("Unpack() error = %v", err)
	}

	if pp.Reserved != 0x00 {
		t.Errorf("Unpack() Reserved = %v, want %v", pp.Reserved, 0x00)
	}
	expectedPayload := []byte{0x01, 0x02, 0x03}
	if !bytes.Equal(pp.Payload, expectedPayload) {
		t.Errorf("Unpack() Payload = %v, want %v", pp.Payload, expectedPayload)
	}
}
