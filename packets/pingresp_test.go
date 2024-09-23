package packets

import (
	"bytes"
	"testing"
)

func TestPingrespPacket_Write(t *testing.T) {
	pr := &PingrespPacket{
		FixedHeader: FixedHeader{
			MessageType: Pingresp,
		},
	}

	var buf bytes.Buffer
	err := pr.Write(&buf)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	expected := []byte{Pingresp << 4, 0}
	if !bytes.Equal(buf.Bytes(), expected) {
		t.Errorf("Write() = %v, want %v", buf.Bytes(), expected)
	}
}

func TestPingrespPacket_Unpack(t *testing.T) {
	pr := &PingrespPacket{}
	err := pr.Unpack(nil)
	if err != nil {
		t.Fatalf("Unpack() error = %v", err)
	}
	// Since Unpack does nothing, we just check that it doesn't return an error
}
