package server

import "testing"

type fakeConn struct {
	id string
}

func (f *fakeConn) DeviceID() string    { return f.id }
func (f *fakeConn) IsActive() bool      { return true }
func (f *fakeConn) Write([]byte) error  { return nil }
func (f *fakeConn) SendDisconnect(byte) {}
func (f *fakeConn) Close()              {}

func TestConnectionStore_GetBeforeRegister(t *testing.T) {
	s := NewConnectionStore()
	if _, ok := s.Get("d1"); ok {
		t.Error("Get() before register = ok, want not ok")
	}
}

func TestConnectionStore_Register(t *testing.T) {
	s := NewConnectionStore()
	c1 := &fakeConn{id: "d1"}
	if prev := s.Register("d1", c1); prev != nil {
		t.Errorf("Register() first returned %v, want nil", prev)
	}
	if got, ok := s.Get("d1"); !ok || got != c1 {
		t.Error("Get() after register mismatch")
	}

	c2 := &fakeConn{id: "d1"}
	if prev := s.Register("d1", c2); prev != c1 {
		t.Errorf("Register() second returned %v, want previous conn", prev)
	}
}

func TestConnectionStore_Remove(t *testing.T) {
	s := NewConnectionStore()
	c1 := &fakeConn{id: "d1"}
	c2 := &fakeConn{id: "d1"}
	s.Register("d1", c1)

	if removed := s.Remove("d1", c2); removed {
		t.Error("Remove() with stale conn = true, want false")
	}
	if _, ok := s.Get("d1"); !ok {
		t.Error("Get() after failed Remove = not ok, want ok")
	}
	if removed := s.Remove("d1", c1); !removed {
		t.Error("Remove() with current conn = false, want true")
	}
	if _, ok := s.Get("d1"); ok {
		t.Error("Get() after Remove = ok, want not ok")
	}
}

func TestConnectionStore_All(t *testing.T) {
	s := NewConnectionStore()
	if all := s.All(); len(all) != 0 {
		t.Errorf("All() len = %d, want 0", len(all))
	}
	s.Register("a", &fakeConn{id: "a"})
	s.Register("b", &fakeConn{id: "b"})
	s.Register("c", &fakeConn{id: "c"})
	all := s.All()
	if len(all) != 3 {
		t.Errorf("All() len = %d, want 3", len(all))
	}
	ids := map[string]bool{}
	for _, c := range all {
		ids[c.DeviceID()] = true
	}
	for _, want := range []string{"a", "b", "c"} {
		if !ids[want] {
			t.Errorf("All() missing device %q", want)
		}
	}
}
