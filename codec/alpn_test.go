package codec

import (
	"crypto/tls"
	"reflect"
	"testing"
)

func TestWithALPN_AddsTokenWhenMissing(t *testing.T) {
	in := &tls.Config{}
	out := WithALPN(in)
	if !reflect.DeepEqual(out.NextProtos, []string{ALPN}) {
		t.Errorf("WithALPN(empty).NextProtos = %v, want [%s]", out.NextProtos, ALPN)
	}
	// The input must never be mutated (it is shared across listeners/attempts).
	if in.NextProtos != nil {
		t.Errorf("WithALPN mutated its input: NextProtos = %v", in.NextProtos)
	}
}

func TestWithALPN_PreservesExistingProtos(t *testing.T) {
	in := &tls.Config{NextProtos: []string{"h3", "rmtt", "h2"}}
	out := WithALPN(in)
	want := []string{"h3", "rmtt", "h2"}
	if !reflect.DeepEqual(out.NextProtos, want) {
		t.Errorf("WithALPN(existing) NextProtos = %v, want %v (rmtt present, order kept)", out.NextProtos, want)
	}
}

func TestWithALPN_PrependsWhenOnlyOtherProtos(t *testing.T) {
	in := &tls.Config{NextProtos: []string{"h2"}}
	out := WithALPN(in)
	want := []string{ALPN, "h2"}
	if !reflect.DeepEqual(out.NextProtos, want) {
		t.Errorf("WithALPN([h2]) NextProtos = %v, want %v", out.NextProtos, want)
	}
}

func TestWithALPN_Nil(t *testing.T) {
	if got := WithALPN(nil); got != nil {
		t.Errorf("WithALPN(nil) = %v, want nil", got)
	}
}

func TestWithALPN_DoesNotAliasInputSlice(t *testing.T) {
	in := &tls.Config{NextProtos: []string{"h2"}}
	out := WithALPN(in)
	out.NextProtos[0] = "mutated"
	if in.NextProtos[0] != "h2" {
		t.Errorf("mutating output changed input: %v", in.NextProtos)
	}
}
