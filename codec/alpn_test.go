package codec

import (
	"crypto/tls"
	"reflect"
	"testing"
)

func TestWithDefaultALPN_AppliesDefaultWhenUnset(t *testing.T) {
	in := &tls.Config{} // NextProtos unset (nil)
	out := WithDefaultALPN(in)
	if !reflect.DeepEqual(out.NextProtos, []string{ALPN}) {
		t.Errorf("WithDefaultALPN(unset).NextProtos = %v, want [%s]", out.NextProtos, ALPN)
	}
	if in.NextProtos != nil {
		t.Errorf("WithDefaultALPN mutated its input: NextProtos = %v", in.NextProtos)
	}
}

func TestWithDefaultALPN_EmptySliceAlsoDefaults(t *testing.T) {
	// Empty now means "use the default", never "disable" — that was the footgun.
	in := &tls.Config{NextProtos: []string{}}
	out := WithDefaultALPN(in)
	if !reflect.DeepEqual(out.NextProtos, []string{ALPN}) {
		t.Errorf("WithDefaultALPN(empty).NextProtos = %v, want [%s]", out.NextProtos, ALPN)
	}
}

func TestWithDefaultALPN_HonoursExplicitOverride(t *testing.T) {
	in := &tls.Config{NextProtos: []string{"h3", "h2"}}
	out := WithDefaultALPN(in)
	if !reflect.DeepEqual(out.NextProtos, []string{"h3", "h2"}) {
		t.Errorf("WithDefaultALPN(override).NextProtos = %v, want [h3 h2]", out.NextProtos)
	}
}

func TestWithDefaultALPN_HonoursExplicitRMTTList(t *testing.T) {
	in := &tls.Config{NextProtos: []string{"rmtt", "h2"}}
	out := WithDefaultALPN(in)
	if !reflect.DeepEqual(out.NextProtos, []string{"rmtt", "h2"}) {
		t.Errorf("WithDefaultALPN([rmtt h2]).NextProtos = %v, want order preserved", out.NextProtos)
	}
}

func TestWithDefaultALPN_DisableSentinel(t *testing.T) {
	in := &tls.Config{NextProtos: []string{DisableALPN}}
	out := WithDefaultALPN(in)
	if out.NextProtos == nil || len(out.NextProtos) != 0 {
		t.Errorf("WithDefaultALPN(DisableALPN).NextProtos = %#v, want empty non-nil slice", out.NextProtos)
	}
	if in.NextProtos[0] != DisableALPN {
		t.Errorf("WithDefaultALPN mutated its input: %v", in.NextProtos)
	}
}

func TestWithDefaultALPN_Nil(t *testing.T) {
	if got := WithDefaultALPN(nil); got != nil {
		t.Errorf("WithDefaultALPN(nil) = %v, want nil", got)
	}
}

func TestWithDefaultALPN_DoesNotAliasInputSlice(t *testing.T) {
	in := &tls.Config{NextProtos: []string{"h2"}}
	out := WithDefaultALPN(in)
	out.NextProtos[0] = "mutated"
	if in.NextProtos[0] != "h2" {
		t.Errorf("mutating output changed input: %v", in.NextProtos)
	}
}
