/*
 * Copyright (c) 2021 IBM Corp and others.
 *
 * All rights reserved. This program and the accompanying materials
 * are made available under the terms of the Eclipse Public License v2.0
 * and Eclipse Distribution License v1.0 which accompany this distribution.
 *
 * The Eclipse Public License is available at
 *    https://www.eclipse.org/legal/epl-2.0/
 * and the Eclipse Distribution License is available at
 *   http://www.eclipse.org/org/documents/edl-v10.php.
 *
 * Contributors:
 *    Matt Brittan
 *    Daichi Tomaru
 */

package RMTT

import (
	"errors"
	"github.com/czqu/rmtt-go/packets"
	"sync"
	"time"
)

type Token interface {
	Wait() bool

	WaitTimeout(time.Duration) bool

	Done() <-chan struct{}

	Error() error
	SetErrorHandler(func(error))
}
type TokenErrorSetter interface {
	setError(error)
}
type tokenCompletor interface {
	Token
	TokenErrorSetter
	flowComplete()
}
type PacketAndToken struct {
	p packets.ControlPacket
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

// Done implements the Token Done method.
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

type ConnectToken struct {
	baseToken
	returnCode byte
}

func (c *ConnectToken) ReturnCode() byte {
	c.m.RLock()
	defer c.m.RUnlock()
	return c.returnCode
}

type DisconnectToken struct {
	baseToken
}

type PushToken struct {
	baseToken
	messageID uint16
}

func newToken(tType byte) tokenCompletor {
	switch tType {
	case packets.Connect:
		return &ConnectToken{baseToken: baseToken{complete: make(chan struct{})}}

	case packets.Push:
		return &PushToken{baseToken: baseToken{complete: make(chan struct{})}}
	case packets.Disconnect:
		return &DisconnectToken{baseToken: baseToken{complete: make(chan struct{})}}
	}
	return nil
}

var TimedOut = errors.New("context canceled")

func WaitTokenTimeout(t Token, d time.Duration) error {
	if !t.WaitTimeout(d) {
		return TimedOut
	}
	return t.Error()
}
