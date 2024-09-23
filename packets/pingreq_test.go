package packets

import (
	"bytes"
	"testing"
)

func TestPingreqPacket_Write(t *testing.T) {
	pr := &PingreqPacket{
		FixedHeader: FixedHeader{
			MessageType: Pingreq,
		},
	}

	var buf bytes.Buffer
	err := pr.Write(&buf)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	expected := []byte{Pingreq << 4, 0}
	if !bytes.Equal(buf.Bytes(), expected) {
		t.Errorf("Write() = %v, want %v", buf.Bytes(), expected)
	}
}

func TestPingreqPacket_Unpack(t *testing.T) {
	pr := &PingreqPacket{}
	err := pr.Unpack(nil)
	if err != nil {
		t.Fatalf("Unpack() error = %v", err)
	}
	// Since Unpack does nothing, we just check that it doesn't return an error
}
