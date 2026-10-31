package client

// White-box regression tests derived from the code review of this repository.
// Each test originally reproduced a defect and now asserts the fixed
// behaviour. Run with:
//
//	go test -v -run 'TestC3_|TestW4_' ./client/

import (
	"errors"
	"testing"
	"time"

	"github.com/czqu/rmtt-go/codec"
)

// TestC3_PushDuringReconnectReturnsError asserts that Push while the client is
// reconnecting completes the token with ErrReconnecting instead of silently
// dropping the payload with Error()==nil.
func TestC3_PushDuringReconnectReturnsError(t *testing.T) {
	o := NewClientOptions()
	o.AutoReconnect = true
	c := NewClient(o).(*client)

	// no connection at all; simulate being mid-reconnect
	c.status.forceConnectionStatus(reconnecting)

	tok := c.Push([]byte("lost payload"))
	completed := tok.WaitTimeout(500 * time.Millisecond)
	if !completed {
		t.Fatal("Push token did not complete")
	}
	if !errors.Is(tok.Error(), ErrReconnecting) {
		t.Fatalf("Push error = %v, want ErrReconnecting (payload must not look sent)", tok.Error())
	}
}

// TestW4_TokenErrorCallbackReentrant asserts that an error handler may call
// back into the token (e.g. Error()) without deadlocking — previously
// setError ran the handler while holding the token mutex.
func TestW4_TokenErrorCallbackReentrant(t *testing.T) {
	tok := newToken(codec.Push)
	tok.SetErrorHandler(func(error) {
		// perfectly natural handler code: inspect the token's error
		_ = tok.Error()
	})

	done := make(chan struct{})
	go func() {
		tok.setError(errors.New("boom"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("setError deadlocked: error handler calling token.Error() blocks on the mutex held by setError")
	}
	if tok.Error() == nil {
		t.Error("Error() = nil, want the set error")
	}
}
