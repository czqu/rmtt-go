package client

import (
	"bytes"
	"testing"

	"github.com/czqu/rmtt-go/codec"
)

func TestMessage_Payload(t *testing.T) {
	p := &codec.PushPacket{Payload: []byte("hello")}
	m := messageFromPush(p, func() {})
	if !bytes.Equal(m.Payload(), []byte("hello")) {
		t.Errorf("Payload() = %q, want %q", m.Payload(), "hello")
	}
}
