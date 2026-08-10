package client

import (
	"errors"
	"testing"
	"time"

	"github.com/czqu/rmtt-go/codec"
)

func TestNewToken(t *testing.T) {
	if _, ok := newToken(codec.Connect).(*ConnectToken); !ok {
		t.Error("newToken(Connect) not *ConnectToken")
	}
	if _, ok := newToken(codec.Push).(*PushToken); !ok {
		t.Error("newToken(Push) not *PushToken")
	}
	if _, ok := newToken(codec.Disconnect).(*DisconnectToken); !ok {
		t.Error("newToken(Disconnect) not *DisconnectToken")
	}
	if newToken(codec.Pingreq) != nil {
		t.Error("newToken(unknown) = non-nil, want nil")
	}
}

func TestBaseToken_WaitTimeout(t *testing.T) {
	tok := newToken(codec.Push)
	if tok.WaitTimeout(10 * time.Millisecond) {
		t.Error("WaitTimeout before completion = true, want false")
	}
	tok.flowComplete()
	if !tok.WaitTimeout(10 * time.Millisecond) {
		t.Error("WaitTimeout after completion = false, want true")
	}
}

func TestBaseToken_Wait(t *testing.T) {
	tok := newToken(codec.Push)
	done := make(chan struct{})
	go func() {
		tok.Wait()
		close(done)
	}()
	select {
	case <-done:
		t.Error("Wait() returned before completion")
	case <-time.After(10 * time.Millisecond):
	}
	tok.flowComplete()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("Wait() did not return after completion")
	}
}

func TestBaseToken_ErrorAndHandler(t *testing.T) {
	expected := errors.New("boom")
	var called error
	tok := newToken(codec.Push)
	tok.SetErrorHandler(func(e error) { called = e })
	tok.setError(expected)
	if tok.Error() != expected {
		t.Errorf("Error() = %v, want %v", tok.Error(), expected)
	}
	if called != expected {
		t.Errorf("errHandler called with %v, want %v", called, expected)
	}
	// flowComplete must be idempotent: a second call must not panic.
	tok.flowComplete()
}

func TestBaseToken_Done(t *testing.T) {
	tok := newToken(codec.Push)
	if tok.Done() == nil {
		t.Fatal("Done() = nil channel")
	}
	select {
	case <-tok.Done():
		t.Error("Done() closed before completion")
	default:
	}
	tok.flowComplete()
	select {
	case <-tok.Done():
	case <-time.After(time.Second):
		t.Error("Done() not closed after completion")
	}
}

func TestConnectToken_ReturnCode(t *testing.T) {
	tok := &ConnectToken{returnCode: codec.Accepted}
	if rc := tok.ReturnCode(); rc != codec.Accepted {
		t.Errorf("ReturnCode() = %d, want %d", rc, codec.Accepted)
	}
}

func TestWaitTokenTimeout(t *testing.T) {
	tok := newToken(codec.Push)
	if err := WaitTokenTimeout(tok, 10*time.Millisecond); !errors.Is(err, TimedOut) {
		t.Errorf("WaitTokenTimeout() = %v, want TimedOut", err)
	}

	expected := errors.New("boom")
	tok2 := newToken(codec.Push)
	tok2.setError(expected)
	if err := WaitTokenTimeout(tok2, 10*time.Millisecond); err != expected {
		t.Errorf("WaitTokenTimeout() = %v, want %v", err, expected)
	}
}
