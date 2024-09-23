package packets

import (
	"bytes"
	"testing"
)

func TestConnackPacket_Write(t *testing.T) {
	ca := &ConnackPacket{
		FixedHeader: FixedHeader{
			MessageType:     Connack,
			RemainingLength: 1,
		},
		ReturnCode: Accepted,
	}

	var buf bytes.Buffer
	err := ca.Write(&buf)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	expected := []byte{Connack << 4, 1, Accepted}
	if !bytes.Equal(buf.Bytes(), expected) {
		t.Errorf("Write() = %v, want %v", buf.Bytes(), expected)
	}
}

func TestConnackPacket_Unpack(t *testing.T) {
	data := []byte{Accepted}
	buf := bytes.NewReader(data)

	ca := &ConnackPacket{}
	err := ca.Unpack(buf)
	if err != nil {
		t.Fatalf("Unpack() error = %v", err)
	}

	if ca.ReturnCode != Accepted {
		t.Errorf("Unpack() = %v, want %v", ca.ReturnCode, Accepted)
	}
}
