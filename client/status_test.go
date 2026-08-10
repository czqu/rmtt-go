package client

import (
	"errors"
	"testing"
	"time"
)

func TestStatus_String(t *testing.T) {
	tests := []struct {
		s    status
		want string
	}{
		{disconnected, "disconnected"},
		{disconnecting, "disconnecting"},
		{connecting, "connecting"},
		{reconnecting, "reconnecting"},
		{connected, "connected"},
		{status(99), "invalid"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("status(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

func waitForStatus(t *testing.T, cs *connectionStatus, want status) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cs.ConnectionStatus() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("status = %s, want %s", cs.ConnectionStatus(), want)
}

func TestStatus_Connecting(t *testing.T) {
	cs := &connectionStatus{}
	if _, err := cs.Connecting(); err != nil {
		t.Fatalf("first Connecting() error = %v", err)
	}
	if cs.ConnectionStatus() != connecting {
		t.Errorf("status = %s, want connecting", cs.ConnectionStatus())
	}

	for _, s := range []status{connected, reconnecting} {
		cs := &connectionStatus{status: s}
		if _, err := cs.Connecting(); !errors.Is(err, errAlreadyConnectedOrReconnecting) {
			t.Errorf("Connecting() from %s error = %v, want errAlreadyConnectedOrReconnecting", s, err)
		}
	}

	cs = &connectionStatus{status: connecting}
	if _, err := cs.Connecting(); !errors.Is(err, errStatusMustBeDisconnected) {
		t.Errorf("Connecting() while connecting error = %v, want errStatusMustBeDisconnected", err)
	}
}

func TestStatus_Connected(t *testing.T) {
	cs := &connectionStatus{}
	fn, err := cs.Connecting()
	if err != nil {
		t.Fatalf("Connecting() error = %v", err)
	}
	if err := fn(true); err != nil {
		t.Fatalf("connected(true) error = %v", err)
	}
	if cs.ConnectionStatus() != connected {
		t.Errorf("status = %s, want connected", cs.ConnectionStatus())
	}

	cs = &connectionStatus{}
	fn, err = cs.Connecting()
	if err != nil {
		t.Fatalf("Connecting() error = %v", err)
	}
	if err := fn(false); err != nil {
		t.Fatalf("connected(false) error = %v", err)
	}
	if cs.ConnectionStatus() != disconnected {
		t.Errorf("status = %s, want disconnected", cs.ConnectionStatus())
	}
}

func TestStatus_Connected_AbortedWhileDisconnecting(t *testing.T) {
	cs := &connectionStatus{}
	fn, err := cs.Connecting()
	if err != nil {
		t.Fatalf("Connecting() error = %v", err)
	}
	done := make(chan struct{})
	var disDone disconnectCompletedFn
	go func() {
		disDone, _ = cs.Disconnecting()
		close(done)
	}()
	waitForStatus(t, cs, disconnecting)
	if err := fn(true); !errors.Is(err, errAbortConnection) {
		t.Errorf("connected(true) error = %v, want errAbortConnection", err)
	}
	<-done
	disDone()
	if cs.ConnectionStatus() != disconnected {
		t.Errorf("status = %s, want disconnected", cs.ConnectionStatus())
	}
}

func TestStatus_Disconnecting(t *testing.T) {
	cs := &connectionStatus{}
	if _, err := cs.Disconnecting(); !errors.Is(err, errAlreadyDisconnected) {
		t.Errorf("Disconnecting() from disconnected error = %v, want errAlreadyDisconnected", err)
	}

	cs = &connectionStatus{}
	fn, err := cs.Connecting()
	if err != nil {
		t.Fatalf("Connecting() error = %v", err)
	}
	if err := fn(true); err != nil {
		t.Fatalf("connected(true) error = %v", err)
	}
	disDone, err := cs.Disconnecting()
	if err != nil {
		t.Fatalf("Disconnecting() error = %v", err)
	}
	if cs.ConnectionStatus() != disconnecting {
		t.Errorf("status = %s, want disconnecting", cs.ConnectionStatus())
	}
	disDone()
	if cs.ConnectionStatus() != disconnected {
		t.Errorf("status = %s, want disconnected", cs.ConnectionStatus())
	}
}

func TestStatus_Disconnecting_WhileConnecting(t *testing.T) {
	cs := &connectionStatus{}
	fn, err := cs.Connecting()
	if err != nil {
		t.Fatalf("Connecting() error = %v", err)
	}
	done := make(chan struct{})
	var disDone disconnectCompletedFn
	var disErr error
	go func() {
		disDone, disErr = cs.Disconnecting()
		close(done)
	}()
	waitForStatus(t, cs, disconnecting)
	if err := fn(true); !errors.Is(err, errAbortConnection) {
		t.Errorf("connected(true) error = %v, want errAbortConnection", err)
	}
	<-done
	if disErr != nil {
		t.Fatalf("Disconnecting() error = %v", disErr)
	}
	disDone()
	if cs.ConnectionStatus() != disconnected {
		t.Errorf("status = %s, want disconnected", cs.ConnectionStatus())
	}
}

func TestStatus_ConnectionLost(t *testing.T) {
	cs := &connectionStatus{}
	if _, err := cs.ConnectionLost(false); !errors.Is(err, errAlreadyDisconnected) {
		t.Errorf("ConnectionLost() from disconnected error = %v, want errAlreadyDisconnected", err)
	}

	cs = &connectionStatus{status: disconnecting}
	if _, err := cs.ConnectionLost(false); !errors.Is(err, errDisconnectionInProgress) {
		t.Errorf("ConnectionLost() from disconnecting error = %v, want errDisconnectionInProgress", err)
	}
}

func TestStatus_ConnectionLost_NoReconnect(t *testing.T) {
	cs := &connectionStatus{}
	fn, err := cs.Connecting()
	if err != nil {
		t.Fatalf("Connecting() error = %v", err)
	}
	if err := fn(true); err != nil {
		t.Fatalf("connected(true) error = %v", err)
	}

	handler, err := cs.ConnectionLost(false)
	if err != nil {
		t.Fatalf("ConnectionLost(false) error = %v", err)
	}
	reConnDone, err := handler(false)
	if err != nil {
		t.Fatalf("handler(false) error = %v", err)
	}
	if reConnDone != nil {
		t.Error("reConnDone = non-nil, want nil")
	}
	if cs.ConnectionStatus() != disconnected {
		t.Errorf("status = %s, want disconnected", cs.ConnectionStatus())
	}
}

func TestStatus_ConnectionLost_Reconnect(t *testing.T) {
	cs := &connectionStatus{}
	fn, err := cs.Connecting()
	if err != nil {
		t.Fatalf("Connecting() error = %v", err)
	}
	if err := fn(true); err != nil {
		t.Fatalf("connected(true) error = %v", err)
	}

	handler, err := cs.ConnectionLost(true)
	if err != nil {
		t.Fatalf("ConnectionLost(true) error = %v", err)
	}
	reConnDone, err := handler(true)
	if err != nil {
		t.Fatalf("handler(true) error = %v", err)
	}
	if reConnDone == nil {
		t.Fatal("reConnDone = nil, want connected fn")
	}
	if cs.ConnectionStatus() != reconnecting {
		t.Errorf("status = %s, want reconnecting", cs.ConnectionStatus())
	}
	if err := reConnDone(true); err != nil {
		t.Fatalf("reConnDone(true) error = %v", err)
	}
	if cs.ConnectionStatus() != connected {
		t.Errorf("status = %s, want connected", cs.ConnectionStatus())
	}
}

func TestStatus_ConnectionLost_ProceedFalse(t *testing.T) {
	cs := &connectionStatus{}
	fn, err := cs.Connecting()
	if err != nil {
		t.Fatalf("Connecting() error = %v", err)
	}
	if err := fn(true); err != nil {
		t.Fatalf("connected(true) error = %v", err)
	}

	handler, err := cs.ConnectionLost(true)
	if err != nil {
		t.Fatalf("ConnectionLost(true) error = %v", err)
	}
	reConnDone, err := handler(false)
	if err != nil {
		t.Errorf("handler(false) error = %v, want nil", err)
	}
	if reConnDone != nil {
		t.Error("reConnDone = non-nil, want nil")
	}
	if cs.ConnectionStatus() != disconnected {
		t.Errorf("status = %s, want disconnected", cs.ConnectionStatus())
	}
}

func TestStatus_ConnectionLost_WhileConnecting(t *testing.T) {
	cs := &connectionStatus{}
	fn, err := cs.Connecting()
	if err != nil {
		t.Fatalf("Connecting() error = %v", err)
	}
	done := make(chan struct{})
	var handler connectionLostHandledFn
	var lostErr error
	go func() {
		handler, lostErr = cs.ConnectionLost(true)
		close(done)
	}()
	waitForStatus(t, cs, disconnecting)
	if err := fn(true); !errors.Is(err, errAbortConnection) {
		t.Errorf("connected(true) error = %v, want errAbortConnection", err)
	}
	<-done
	if lostErr != nil {
		t.Fatalf("ConnectionLost(true) error = %v", lostErr)
	}
	reConnDone, err := handler(true)
	if err != nil {
		t.Fatalf("handler(true) error = %v", err)
	}
	if cs.ConnectionStatus() != reconnecting {
		t.Errorf("status = %s, want reconnecting", cs.ConnectionStatus())
	}
	if err := reConnDone(true); err != nil {
		t.Fatalf("reConnDone(true) error = %v", err)
	}
	if cs.ConnectionStatus() != connected {
		t.Errorf("status = %s, want connected", cs.ConnectionStatus())
	}
}

func TestStatus_ForceConnectionStatus(t *testing.T) {
	cs := &connectionStatus{}
	cs.forceConnectionStatus(reconnecting)
	if cs.ConnectionStatus() != reconnecting {
		t.Errorf("status = %s, want reconnecting", cs.ConnectionStatus())
	}
}
