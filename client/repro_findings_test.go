package client

// White-box repro tests confirming code-review findings. Each test logs
// "REPRO CONFIRMED" when the buggy behaviour is observed. Run with:
//
//	go test -v -run TestRepro ./client/

import (
	"errors"
	"testing"
	"time"

	"github.com/czqu/rmtt-go/codec"
)

// TestRepro_C3_PushDuringReconnectSilentlyDropped shows that while the client
// is in the reconnecting state (AutoReconnect enabled), Push completes the
// token with Error()==nil although not a single byte is written — the payload
// is silently dropped.
func TestRepro_C3_PushDuringReconnectSilentlyDropped(t *testing.T) {
	o := NewClientOptions()
	o.AutoReconnect = true
	c := NewClient(o).(*client)

	// no connection at all; simulate being mid-reconnect
	c.status.forceConnectionStatus(reconnecting)

	tok := c.Push([]byte("lost payload"))
	completed := tok.WaitTimeout(500 * time.Millisecond)
	err := tok.Error()

	t.Logf("Push during reconnecting: token completed=%v, Error()=%v, IsConnected()=%v, c.conn=%v",
		completed, err, c.IsConnected(), c.conn)

	if completed && err == nil && c.conn == nil {
		t.Logf("REPRO CONFIRMED: Push finished with Error()==nil while c.conn==nil — payload silently dropped, caller believes it was sent")
	} else {
		t.Logf("NOT REPRODUCED: completed=%v err=%v conn=%v", completed, err, c.conn)
	}
}

// TestRepro_W4_TokenErrorCallbackDeadlock shows that baseToken.setError runs
// the user's error handler while holding the token mutex, so a handler that
// reads token.Error() deadlocks (sync.RWMutex is not reentrant).
func TestRepro_W4_TokenErrorCallbackDeadlock(t *testing.T) {
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
		t.Logf("NOT REPRODUCED: setError returned, no deadlock")
	case <-time.After(1 * time.Second):
		t.Logf("REPRO CONFIRMED: setError deadlocked for 1s — error handler calling token.Error() blocks on the mutex held by setError (goroutine leaked holding the lock)")
	}
}
