package client

import (
	"errors"
	"sync"
	"time"

	"github.com/czqu/rmtt-go/codec"
)

// Token tracks the completion of an asynchronous operation (Connect, Push,
// Disconnect). Wait for completion, then check Error.
type Token interface {
	Wait() bool
	WaitTimeout(time.Duration) bool
	Done() <-chan struct{}
	Error() error
	SetErrorHandler(func(error))
}

// TokenErrorSetter is implemented by tokens that can carry an error.
type TokenErrorSetter interface {
	setError(error)
}

type tokenCompletor interface {
	Token
	TokenErrorSetter
	flowComplete()
}

// PacketAndToken pairs a control packet with the token tracking its
// completion; used for the internal outbound queues.
type PacketAndToken struct {
	p codec.ControlPacket
	t tokenCompletor
}

type baseToken struct {
	m          sync.RWMutex
	complete   chan struct{}
	err        error
	errHandler func(error)
}

func (b *baseToken) Wait() bool {
	<-b.complete
	return true
}

func (b *baseToken) WaitTimeout(d time.Duration) bool {
	timer := time.NewTimer(d)
	select {
	case <-b.complete:
		if !timer.Stop() {
			<-timer.C
		}
		return true
	case <-timer.C:
	}
	return false
}

func (b *baseToken) Done() <-chan struct{} {
	return b.complete
}

func (b *baseToken) Error() error {
	b.m.RLock()
	defer b.m.RUnlock()
	return b.err
}

func (b *baseToken) flowComplete() {
	select {
	case <-b.complete:
	default:
		close(b.complete)
	}
}

func (b *baseToken) setError(e error) {
	b.m.Lock()
	b.HandlerError(e)
	b.err = e
	b.flowComplete()
	b.m.Unlock()
}

func (b *baseToken) HandlerError(e error) {
	if b.errHandler != nil {
		b.errHandler(e)
	}
}

func (b *baseToken) SetErrorHandler(f func(error)) {
	b.errHandler = f
}

// ConnectToken completes when the CONNECT handshake finishes; ReturnCode
// holds the server's CONNACK return code.
type ConnectToken struct {
	baseToken
	returnCode byte
}

// ReturnCode returns the CONNACK return code received from the server.
func (c *ConnectToken) ReturnCode() byte {
	c.m.RLock()
	defer c.m.RUnlock()
	return c.returnCode
}

// DisconnectToken completes when the DISCONNECT packet has been sent.
type DisconnectToken struct {
	baseToken
}

// PushToken completes when the PUSH packet has been written to the
// connection.
type PushToken struct {
	baseToken
	messageID uint16
}

func newToken(tType byte) tokenCompletor {
	switch tType {
	case codec.Connect:
		return &ConnectToken{baseToken: baseToken{complete: make(chan struct{})}}
	case codec.Push:
		return &PushToken{baseToken: baseToken{complete: make(chan struct{})}}
	case codec.Disconnect:
		return &DisconnectToken{baseToken: baseToken{complete: make(chan struct{})}}
	}
	return nil
}

// TimedOut is returned by WaitTokenTimeout when the token did not complete
// within the given duration.
var TimedOut = errors.New("context canceled")

// WaitTokenTimeout waits up to d for the token to complete and returns its
// error, or TimedOut if it did not complete in time.
func WaitTokenTimeout(t Token, d time.Duration) error {
	if !t.WaitTimeout(d) {
		return TimedOut
	}
	return t.Error()
}
