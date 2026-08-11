package main

import "testing"

func TestSimpleAuth_Authenticate(t *testing.T) {
	a := &simpleAuth{}
	id, ok := a.Authenticate("dev-001")
	if !ok {
		t.Error("Authenticate() ok = false, want true")
	}
	if id != "dev-001" {
		t.Errorf("Authenticate() id = %q, want %q", id, "dev-001")
	}
}

func TestSimpleListener_Events(t *testing.T) {
	l := &simpleListener{}
	l.OnConnectionEstablished("dev-001")
	l.OnConnectionClosed("dev-001", "test")
}
