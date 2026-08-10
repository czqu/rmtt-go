package server

import "testing"

func TestNewServerOptions_Defaults(t *testing.T) {
	o := NewServerOptions()
	if o.Port != 18883 {
		t.Errorf("Port = %d, want 18883", o.Port)
	}
	if o.KeepalivePolicy == nil {
		t.Error("KeepalivePolicy = nil")
	}
	if len(o.listeners) != 0 {
		t.Errorf("listeners len = %d, want 0", len(o.listeners))
	}
}

func TestServerOptions_AddListenerAndSetters(t *testing.T) {
	o := NewServerOptions()

	l := NewTCPListener(":18883")
	o.AddListener(l)
	if len(o.listeners) != 1 || o.listeners[0] != l {
		t.Error("AddListener() did not append the listener")
	}

	o.SetPort(18884)
	if o.Port != 18884 {
		t.Errorf("Port = %d, want 18884", o.Port)
	}

	auth := fakeAuth{}
	o.SetAuthenticator(auth)
	if o.Authenticator != auth {
		t.Error("SetAuthenticator() not stored")
	}

	handler := func(string, []byte) {}
	o.SetMessageHandler(handler)
	if o.MessageHandler == nil {
		t.Error("SetMessageHandler() not stored")
	}

	cl := fakeConnListener{}
	o.SetConnectionListener(cl)
	if o.ConnectionListener != cl {
		t.Error("SetConnectionListener() not stored")
	}

	kp := DefaultKeepalivePolicy()
	o.SetKeepalivePolicy(kp)
	if o.KeepalivePolicy != kp {
		t.Error("SetKeepalivePolicy() not stored")
	}
}
