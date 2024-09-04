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
	"sync"
)

type status uint32

const (
	disconnected  status = iota // default (nil) status is disconnected
	disconnecting               // Transitioning from one of the below states back to disconnected
	connecting
	reconnecting
	connected
)

func (s status) String() string {
	switch s {
	case disconnected:
		return "disconnected"
	case disconnecting:
		return "disconnecting"
	case connecting:
		return "connecting"
	case reconnecting:
		return "reconnecting"
	case connected:
		return "connected"
	default:
		return "invalid"
	}
}

type connCompletedFn func(success bool) error
type disconnectCompletedFn func()
type connectionLostHandledFn func(bool) (connCompletedFn, error)

var (
	errAbortConnection                = errors.New("disconnect called whist connection attempt in progress")
	errAlreadyConnectedOrReconnecting = errors.New("status is already connected or reconnecting")
	errStatusMustBeDisconnected       = errors.New("status can only transition to connecting from disconnected")
	errAlreadyDisconnected            = errors.New("status is already disconnected")
	errDisconnectionRequested         = errors.New("disconnection was requested whilst the action was in progress")
	errDisconnectionInProgress        = errors.New("disconnection already in progress")
	errAlreadyHandlingConnectionLoss  = errors.New("status is already Connection Lost")
	errConnLossWhileDisconnecting     = errors.New("connection status is disconnecting so loss of connection is expected")
)

type connectionStatus struct {
	sync.RWMutex
	status        status
	willReconnect bool

	actionCompleted chan struct{}
}

func (c *connectionStatus) ConnectionStatus() status {
	c.RLock()
	defer c.RUnlock()
	return c.status
}
func (c *connectionStatus) ConnectionStatusRetry() (status, bool) {
	c.RLock()
	defer c.RUnlock()
	return c.status, c.willReconnect
}

func (c *connectionStatus) Connecting() (connCompletedFn, error) {
	c.Lock()
	defer c.Unlock()

	if c.status == connected || c.status == reconnecting {
		return nil, errAlreadyConnectedOrReconnecting
	}
	if c.status != disconnected {
		return nil, errStatusMustBeDisconnected
	}
	c.status = connecting
	c.actionCompleted = make(chan struct{})
	return c.connected, nil
}
func (c *connectionStatus) connected(success bool) error {
	c.Lock()
	defer func() {
		close(c.actionCompleted)
		c.actionCompleted = nil
		c.Unlock()
	}()

	if c.status == disconnecting {
		return errAbortConnection
	}
	if success {
		c.status = connected
	} else {
		c.status = disconnected
	}
	return nil
}
func (c *connectionStatus) Disconnecting() (disconnectCompletedFn, error) {
	c.Lock()
	if c.status == disconnected {
		c.Unlock()
		return nil, errAlreadyDisconnected // May not always be treated as an error
	}
	if c.status == disconnecting { // Need to wait for existing process to complete
		c.willReconnect = false // Ensure that the existing disconnect process will not reconnect
		disConnectDone := c.actionCompleted
		c.Unlock()
		<-disConnectDone                   // Wait for existing operation to complete
		return nil, errAlreadyDisconnected // Well we are now!
	}

	prevStatus := c.status
	c.status = disconnecting

	// We may need to wait for connection/reconnection process to complete (they should regularly check the status)
	if prevStatus == connecting || prevStatus == reconnecting {
		connectDone := c.actionCompleted
		c.Unlock() // Safe because the only way to leave the disconnecting status is via this function
		<-connectDone

		if prevStatus == reconnecting && !c.willReconnect {
			return nil, errAlreadyDisconnected // Following connectionLost process we will be disconnected
		}
		c.Lock()
	}
	c.actionCompleted = make(chan struct{})
	c.Unlock()
	return c.disconnectionCompleted, nil
}
func (c *connectionStatus) disconnectionCompleted() {
	c.Lock()
	defer c.Unlock()
	c.status = disconnected
	close(c.actionCompleted) // Alert anything waiting on the connection process to complete
	c.actionCompleted = nil
}
func (c *connectionStatus) ConnectionLost(willReconnect bool) (connectionLostHandledFn, error) {
	c.Lock()
	defer c.Unlock()
	if c.status == disconnected {
		return nil, errAlreadyDisconnected
	}
	if c.status == disconnecting { // its expected that connection lost will be called during the disconnection process
		return nil, errDisconnectionInProgress
	}

	c.willReconnect = willReconnect
	prevStatus := c.status
	c.status = disconnecting

	if prevStatus == connecting || prevStatus == reconnecting {
		connectDone := c.actionCompleted
		c.Unlock()
		<-connectDone
		c.Lock()
		if !willReconnect {
			// In this case the connection will always be aborted so there is nothing more for us to do
			return nil, errAlreadyDisconnected
		}
	}
	c.actionCompleted = make(chan struct{})

	return c.getConnectionLostHandler(willReconnect), nil
}
func (c *connectionStatus) getConnectionLostHandler(reconnectRequested bool) connectionLostHandledFn {
	return func(proceed bool) (connCompletedFn, error) {

		c.Lock()
		defer c.Unlock()

		if !c.willReconnect || !proceed {
			c.status = disconnected
			close(c.actionCompleted) // Alert anything waiting on the connection process to complete
			c.actionCompleted = nil
			if !reconnectRequested || !proceed {
				return nil, nil
			}
			return nil, errDisconnectionRequested
		}

		c.status = reconnecting
		return c.connected, nil
	}
}
func (c *connectionStatus) forceConnectionStatus(s status) {
	c.Lock()
	defer c.Unlock()
	c.status = s
}
