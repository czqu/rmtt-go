package codec

import (
	"bytes"
	"testing"
)

func TestNewControlPacket(t *testing.T) {
	for _, mt := range []byte{Connect, Connack, Push, Pingreq, Pingresp, Disconnect} {
		if NewControlPacket(mt) == nil {
			t.Errorf("NewControlPacket(%d) = nil", mt)
		}
	}
	if NewControlPacket(0x00) != nil {
		t.Error("NewControlPacket(unknown) = non-nil, want nil")
	}
}

func TestNewControlPacketWithHeader(t *testing.T) {
	for _, mt := range []byte{Connect, Connack, Push, Pingreq, Pingresp, Disconnect} {
		cp, err := NewControlPacketWithHeader(FixedHeader{MessageType: mt})
		if err != nil || cp == nil {
			t.Errorf("NewControlPacketWithHeader(%d) error = %v", mt, err)
		}
	}
	if _, err := NewControlPacketWithHeader(FixedHeader{MessageType: 0x09}); err == nil {
		t.Error("NewControlPacketWithHeader(unknown) error = nil, want error")
	}
}

func TestFixedHeader_Unpack_NonZeroFlags(t *testing.T) {
	var fh FixedHeader
	if err := fh.unpack(Connect<<4|0x01, bytes.NewReader([]byte{0})); err == nil {
		t.Error("unpack() with non-zero flags error = nil, want error")
	}
}

func TestDecodeLength_MaxFourBytes(t *testing.T) {
	n, err := decodeLength(bytes.NewReader([]byte{0xFF, 0xFF, 0xFF, 0x7F}))
	if err != nil {
		t.Fatalf("decodeLength() error = %v", err)
	}
	if n != 268435455 {
		t.Errorf("decodeLength() = %d, want 268435455", n)
	}
}

func TestPushPacket_Unpack_EmptyPayload(t *testing.T) {
	pp := &PushPacket{FixedHeader: FixedHeader{RemainingLength: 0}}
	if err := pp.Unpack(bytes.NewReader([]byte{0x00})); err == nil {
		t.Error("Unpack() with RemainingLength 0 error = nil, want error")
	}
}

func TestDisconnectPacket_SetReturnCode(t *testing.T) {
	d := &DisconnectPacket{}
	d.SetReturnCode(DiscServerShutdown)
	if got := d.GetReturnCode(); got != DiscServerShutdown {
		t.Errorf("GetReturnCode() = 0x%x, want DiscServerShutdown", got)
	}
}

func roundTrip(t *testing.T, in ControlPacket) ControlPacket {
	t.Helper()
	var buf bytes.Buffer
	if err := in.Write(&buf); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	out, err := ReadPacket(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("ReadPacket() error = %v", err)
	}
	return out
}

func TestRoundTrip_Connect(t *testing.T) {
	in := &ConnectPacket{
		FixedHeader:     FixedHeader{MessageType: Connect},
		MagicNumber:     0x637a7175,
		ProtocolVersion: 1,
		Keepalive:       60,
		Credential:      "dev-001",
	}
	out := roundTrip(t, in).(*ConnectPacket)
	if out.MagicNumber != in.MagicNumber {
		t.Errorf("MagicNumber = 0x%x, want 0x%x", out.MagicNumber, in.MagicNumber)
	}
	if out.ProtocolVersion != in.ProtocolVersion {
		t.Errorf("ProtocolVersion = %d, want %d", out.ProtocolVersion, in.ProtocolVersion)
	}
	if out.Keepalive != in.Keepalive {
		t.Errorf("Keepalive = %d, want %d", out.Keepalive, in.Keepalive)
	}
	if out.Credential != in.Credential {
		t.Errorf("Credential = %q, want %q", out.Credential, in.Credential)
	}
}

func TestRoundTrip_Connack(t *testing.T) {
	in := &ConnackPacket{
		FixedHeader:     FixedHeader{MessageType: Connack},
		ReturnCode:      Accepted,
		ServerKeepalive: 60,
	}
	out := roundTrip(t, in).(*ConnackPacket)
	if out.ReturnCode != Accepted || out.ServerKeepalive != 60 {
		t.Errorf("round trip mismatch: ReturnCode=%d ServerKeepalive=%d", out.ReturnCode, out.ServerKeepalive)
	}
}

func TestRoundTrip_Push(t *testing.T) {
	in := &PushPacket{
		FixedHeader: FixedHeader{MessageType: Push},
		Payload:     []byte("data"),
	}
	out := roundTrip(t, in).(*PushPacket)
	if !bytes.Equal(out.Payload, []byte("data")) {
		t.Errorf("Payload = %q, want %q", out.Payload, "data")
	}
}

func TestRoundTrip_Pingreq(t *testing.T) {
	in := &PingreqPacket{FixedHeader: FixedHeader{MessageType: Pingreq}}
	if _, ok := roundTrip(t, in).(*PingreqPacket); !ok {
		t.Error("round trip did not yield *PingreqPacket")
	}
}

func TestRoundTrip_Pingresp(t *testing.T) {
	in := &PingrespPacket{FixedHeader: FixedHeader{MessageType: Pingresp}}
	if _, ok := roundTrip(t, in).(*PingrespPacket); !ok {
		t.Error("round trip did not yield *PingrespPacket")
	}
}

func TestRoundTrip_Disconnect(t *testing.T) {
	in := &DisconnectPacket{FixedHeader: FixedHeader{MessageType: Disconnect}}
	in.SetReturnCode(DiscKeepaliveTimeout)
	out := roundTrip(t, in).(*DisconnectPacket)
	if out.GetReturnCode() != DiscKeepaliveTimeout {
		t.Errorf("GetReturnCode() = 0x%x, want DiscKeepaliveTimeout", out.GetReturnCode())
	}
}
