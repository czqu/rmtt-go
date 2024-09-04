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
	"bytes"
	"container/list"
	"errors"
	"fmt"
	"github.com/czqu/rmtt-go/packets"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"time"
)

type Client interface {
	IsConnected() bool

	Connect() Token
	Push(payload interface{}) Token
	Disconnect(quiesce uint)
	AddPayloadHandlerLast(handler MessageHandler)
}

type client struct {
	options        ClientOptions
	payloadHandler *handler
	status         connectionStatus
	conn           net.Conn
	connMu         sync.Mutex
	stop           chan struct{}
	lastSent       atomic.Value // Record that a packet has been sent to server
	lastReceived   atomic.Value
	workers        sync.WaitGroup
	obound         chan *PacketAndToken // outgoing push packet
	oboundP        chan *PacketAndToken // outgoing 'priority' packet (anything other than publish)
	commsStopped   chan struct{}
	backoff        *backoffController
}

func NewClient(o *ClientOptions) Client {
	c := &client{}
	c.options = *o

	switch c.options.ProtocolVersion {
	case 1:
	default:
		c.options.ProtocolVersion = 1
	}
	c.payloadHandler = newHandler()
	c.obound = make(chan *PacketAndToken)
	c.oboundP = make(chan *PacketAndToken)
	c.backoff = newBackoffController()
	return c
}
func (c *client) Connect() Token {

	t := newToken(packets.Connect).(*ConnectToken)
	connectionUp, err := c.status.Connecting()
	if err != nil {
		if err == errAlreadyConnectedOrReconnecting && c.options.AutoReconnect {
			WARN.Println("Connect() called but not disconnected")
			t.returnCode = packets.Accepted
			t.flowComplete()
			return t
		}
		ERROR.Println(err) // CONNECT should never be called unless we are disconnected
		t.setError(err)
		return t
	}
	go func() {
		if c.options.Servers == nil || len(c.options.Servers) == 0 {
			t.setError(fmt.Errorf(CLI, "no server  to connect to"))
			if err := connectionUp(false); err != nil {
				ERROR.Println(err.Error())
			}
			return
		}
		var conn net.Conn
		var rc byte
		var err error
		for {

			conn, rc, err = c.attemptConnection()
			if err != nil {
				if c.options.ConnectRetry && !errors.Is(err, ProtocolViolationErr) && !errors.Is(err, RefusedNotAuthorisedErr) {
					DEBUG.Println("Connect failed, sleeping for", int(c.options.ConnectRetryInterval.Seconds()), "seconds and will then retry, error:", err.Error())
					time.Sleep(c.options.ConnectRetryInterval)

					if c.status.ConnectionStatus() == connecting {
						continue
					}
				}
				ERROR.Println(CLI, "Failed to connect to a server")
				t.returnCode = rc
				t.setError(err)
				if err := connectionUp(false); err != nil {
					ERROR.Println(err.Error())
				}
				return
			}
			break
		}

		inboundFromStore := make(chan packets.ControlPacket)
		if !c.startWorkers(conn, connectionUp, inboundFromStore) { // note that this takes care of updating the status (to connected or disconnected)

			WARN.Println(CLI, "Connect() called but connection established in another goroutine")
		}

		close(inboundFromStore)
		t.flowComplete()
		DEBUG.Println(CLI, "exit startClient")

	}()
	return t
}

func (c *client) startWorkers(conn net.Conn, connectionUp connCompletedFn, inboundFromStore <-chan packets.ControlPacket) bool {
	c.connMu.Lock()
	defer c.connMu.Unlock()
	if c.conn != nil {
		WARN.Println(CLI, "already running")
		_ = conn.Close()
		if err := connectionUp(false); err != nil {
			ERROR.Println(err.Error())
		}
		return false
	}
	c.conn = conn
	c.stop = make(chan struct{})
	if c.options.Heartbeat != 0 {
		c.lastReceived.Store(time.Now())
		c.lastSent.Store(time.Now())
		c.workers.Add(1)
		go keepalive(c, conn)
	}
	incomingPubChan := make(chan *packets.PushPacket)
	c.workers.Add(1)
	ackOut := c.payloadHandler.dispatch(incomingPubChan, c)
	if err := connectionUp(true); err != nil {
		ERROR.Println(err)
	}
	commsobound := make(chan *PacketAndToken)
	commsoboundP := make(chan *PacketAndToken)
	c.workers.Add(1)
	go func() {
		defer c.workers.Done()
		for {
			select {
			case msg := <-c.oboundP:
				commsoboundP <- msg
			case msg := <-c.obound:
				commsobound <- msg
			case msg, ok := <-ackOut:
				if !ok {
					ackOut = nil     // ignore channel going forward
					c.workers.Done() // matchAndDispatch has completed
					continue         // await next message
				}
				commsoboundP <- msg
			case <-c.stop:
				// Attempt to transmit any outstanding acknowledgements (this may well fail but should work if this is a clean disconnect)
				if ackOut != nil {
					for msg := range ackOut {
						commsoboundP <- msg
					}
					c.workers.Done() // matchAndDispatch has completed
				}
				close(commsoboundP) // Nothing sending to these channels anymore so close them and allow comms routines to exit
				close(commsobound)
				DEBUG.Println(CLI, "startCommsWorkers output redirector finished")
				return
			}
		}
	}()

	commsIncomingPub, commsErrors := startComms(c.conn, c, inboundFromStore, commsoboundP, commsobound)
	c.commsStopped = make(chan struct{})
	go func() {
		for {
			if commsIncomingPub == nil && commsErrors == nil {
				break
			}
			select {
			case pub, ok := <-commsIncomingPub:
				if !ok {
					// Incoming comms has shutdown
					close(incomingPubChan) // stop the router
					commsIncomingPub = nil
					continue
				}
				// Care is needed here because an error elsewhere could trigger a deadlock
			sendPubLoop:
				for {
					select {
					case incomingPubChan <- pub:
						break sendPubLoop
					case err, ok := <-commsErrors:
						if !ok { // commsErrors has been closed so we can ignore it
							commsErrors = nil
							continue
						}
						ERROR.Println(CLI, "Connect comms goroutine - error triggered during send Pub", err)
						c.internalConnLost(err)
						continue
					}
				}
			case err, ok := <-commsErrors:
				if !ok {
					commsErrors = nil
					continue
				}
				ERROR.Println(CLI, "Connect comms goroutine - error triggered", err)
				c.internalConnLost(err)
				continue
			}
		}
		DEBUG.Println(CLI, "incoming comms goroutine done")
		close(c.commsStopped)
	}()
	DEBUG.Println(CLI, "startCommsWorkers done")
	return true
}
func newConnectMsgFromOptions(options *ClientOptions, broker *url.URL) *packets.ConnectPacket {
	m := packets.NewControlPacket(packets.Connect).(*packets.ConnectPacket)

	m.MagicNumber = 0x637a7175
	m.ClientIdentifier = options.ClientID
	m.Keepalive = uint16(options.Heartbeat)
	return m
}

var (
	RefusedNotAuthorisedErr = errors.New("The server has rejected our request. Please check your permissions")
	ProtocolViolationErr    = errors.New("The server has rejected our request. Please check your permissions")
)

func (c *client) attemptConnection() (net.Conn, byte, error) {
	protocolVersion := c.options.ProtocolVersion
	var (
		conn net.Conn
		err  error
		rc   byte
	)
	servers := c.options.Servers
	var server *url.URL
	for _, server = range servers {
		cm := newConnectMsgFromOptions(&c.options, server)
		connDeadline := time.Now().Add(c.options.ConnectTimeout)
		dialer := &net.Dialer{Timeout: c.options.ConnectTimeout}
		DEBUG.Println(CLI, "Attempting connection to server", server)
		conn, err = openConnection(server, dialer)
		if err != nil {
			ERROR.Println(err.Error())
			WARN.Println(CLI, "failed to connect to server, trying next")
			rc = packets.ErrNetworkError
			continue
		}
		if err := conn.SetDeadline(connDeadline); err != nil {
			ERROR.Println("set deadline for handshake ", err)
		}
		rc, err = connectServer(conn, cm, protocolVersion)
		if rc == packets.Accepted {
			if err := conn.SetDeadline(time.Time{}); err != nil {
				ERROR.Println("reset deadline following handshake ", err)
			}
			c.options.ProtocolVersion = protocolVersion
			break
		}

	}
	if rc == packets.ErrNetworkError {
		WARN.Println("Failed to connect to any server")
		return conn, rc, err
	}
	if rc == packets.Accepted {
		DEBUG.Println(CLI, "connected to server ", server)
		return conn, rc, err
	}
	if rc == packets.ErrRefusedNotAuthorised {
		ERROR.Println(CLI, "The server has rejected our request. Please check your permissions")
		c.Disconnect(100)
		err = RefusedNotAuthorisedErr
		return conn, rc, err
	}
	if rc == packets.ErrProtocolViolation {
		ERROR.Println(CLI, "Unsupported server protocol version ")
		err = ProtocolViolationErr
		c.Disconnect(100)
		return conn, rc, err
	}

	return conn, rc, err
}
func (c *client) disconnect() {
	done := c.stopCommsWorkers()
	if done != nil {
		<-done // Wait until the disconnect is complete (to limit chance that another connection will be started)
		DEBUG.Println(CLI, "forcefully disconnecting")
		DEBUG.Println(CLI, "disconnected")
	}
}
func (c *client) stopCommsWorkers() chan struct{} {
	DEBUG.Println(CLI, "stopCommsWorkers called")
	// It is possible that this function will be called multiple times simultaneously due to the way things get shutdown
	c.connMu.Lock()
	if c.conn == nil {
		DEBUG.Println(CLI, "stopCommsWorkers done (not running)")
		c.connMu.Unlock()
		return nil
	}

	// It is important that everything is stopped in the correct order to avoid deadlocks. The main issue here is
	// the router because it both receives incoming publish messages and also sends outgoing acknowledgements. To
	// avoid issues we signal the workers to stop and close the connection (it is probably already closed but
	// there is no harm in being sure). We can then wait for the workers to finnish before closing outbound comms
	// channels which will allow the comms routines to exit.

	// We stop all non-comms related workers first (ping, keepalive, errwatch, resume etc) so they don't get blocked waiting on comms
	close(c.stop)     // Signal for workers to stop
	c.conn.Close()    // Possible that this is already closed but no harm in closing again
	c.conn = nil      // Important that this is the only place that this is set to nil
	c.connMu.Unlock() // As the connection is now nil we can unlock the mu (allowing subsequent calls to exit immediately)

	doneChan := make(chan struct{})

	go func() {
		DEBUG.Println(CLI, "stopCommsWorkers waiting for workers")
		c.workers.Wait()

		// Stopping the workers will allow the comms routines to exit; we wait for these to complete
		DEBUG.Println(CLI, "stopCommsWorkers waiting for comms")
		<-c.commsStopped // wait for comms routine to stop

		DEBUG.Println(CLI, "stopCommsWorkers done")
		close(doneChan)
	}()
	return doneChan
}
func (c *client) Disconnect(quiesce uint) {
	done := make(chan struct{}) // Simplest way to ensure quiesce is always honoured
	go func() {
		defer close(done)
		disDone, err := c.status.Disconnecting()
		if err != nil {
			// Status has been set to disconnecting, but we had to wait for something else to complete
			WARN.Println(CLI, err.Error())
			return
		}
		defer func() {
			c.disconnect() // Force disconnection
			disDone()      // Update status
		}()
		DEBUG.Println(CLI, "disconnecting")
		dm := packets.NewControlPacket(packets.Disconnect).(*packets.DisconnectPacket)
		dt := newToken(packets.Disconnect)
		select {
		case c.oboundP <- &PacketAndToken{p: dm, t: dt}:
			// wait for work to finish, or quiesce time consumed
			DEBUG.Println(CLI, "calling WaitTimeout")
			dt.WaitTimeout(time.Duration(quiesce) * time.Millisecond)
			DEBUG.Println(CLI, "WaitTimeout done")
		// Below code causes a potential data race. Following status refactor it should no longer be required
		// but leaving in as need to check code further.
		// case <-c.commsStopped:
		//           WARN.Println("Disconnect packet could not be sent because comms stopped")
		case <-time.After(time.Duration(quiesce) * time.Millisecond):
			WARN.Println("Disconnect packet not sent due to timeout")
		}
	}()

	// Return when done or after timeout expires (would like to change but this maintains compatibility)
	delay := time.NewTimer(time.Duration(quiesce) * time.Millisecond)
	select {
	case <-done:
		if !delay.Stop() {
			<-delay.C
		}
	case <-delay.C:
	}
}
func (c *client) IsConnected() bool {
	s, r := c.status.ConnectionStatusRetry()
	switch {
	case s == connected:
		return true
	case c.options.ConnectRetry && s == connecting:
		return true
	case c.options.AutoReconnect:
		return s == reconnecting || (s == disconnecting && r) // r indicates we will reconnect
	default:
		return false

	}
}

var ErrNotConnected = errors.New("not Connected")

func (c *client) Push(payload interface{}) Token {
	token := newToken(packets.Push).(*PushToken)
	DEBUG.Println("enter Push")
	switch {
	case !c.IsConnected():
		token.setError(ErrNotConnected)
		return token
	case c.status.ConnectionStatus() == reconnecting:
		token.flowComplete()
		return token
	}
	pub := packets.NewControlPacket(packets.Push).(*packets.PushPacket)
	switch p := payload.(type) {
	case string:
		pub.Payload = []byte(p)
	case []byte:
		pub.Payload = p
	case bytes.Buffer:
		pub.Payload = p.Bytes()
	default:
		token.setError(fmt.Errorf("unknown payload type"))
		return token
	}

	DEBUG.Println("sending  message")
	pushWaitTimeout := c.options.WriteTimeout
	if pushWaitTimeout == 0 {
		pushWaitTimeout = time.Second * 30
	}

	t := time.NewTimer(pushWaitTimeout)
	defer t.Stop()
	select {
	case c.obound <- &PacketAndToken{p: pub, t: token}:
		INFO.Println(CLI, "send")
	case <-t.C:
		INFO.Println(CLI, "err")
		token.setError(errors.New("push was broken by timeout"))
	}

	return token

}

type MessageHandler func(Client, Message)
type handler struct {
	sync.RWMutex
	handlers *list.List
	messages chan *packets.PushPacket
}

func newHandler() *handler {
	router := &handler{handlers: list.New(), messages: make(chan *packets.PushPacket)}
	return router
}
func (h *handler) AddLast(handler MessageHandler) {
	h.Lock()
	defer h.Unlock()
	h.handlers.PushBack(handler)

}

func (h *handler) dispatch(messages <-chan *packets.PushPacket, client *client) <-chan *PacketAndToken {
	var wg sync.WaitGroup
	ackOutChan := make(chan *PacketAndToken) // Channel returned to caller; closed when messages channel closed
	var ackInChan chan *PacketAndToken       // ACKs generated by ackFunc get put onto this channel
	stopAckCopy := make(chan struct{})       // Closure requests stop of go routine copying ackInChan to ackOutChan
	ackCopyStopped := make(chan struct{})    // Closure indicates that it is safe to close ackOutChan
	goRoutinesDone := make(chan struct{})    // closed on wg.Done()
	ackInChan = make(chan *PacketAndToken)
	go func() { // go routine to copy from ackInChan to ackOutChan until stopped
		for {
			select {
			case a := <-ackInChan:
				ackOutChan <- a
			case <-stopAckCopy:
				close(ackCopyStopped) // Signal main go routine that it is safe to close ackOutChan
				for {
					select {
					case <-ackInChan: // drain ackInChan to ensure all goRoutines can complete cleanly (ACK dropped)
						DEBUG.Println("Dispatch received acknowledgment after processing stopped (ACK dropped).")
					case <-goRoutinesDone:
						close(ackInChan) // Nothing further should be sent (a panic is probably better than silent failure)
						DEBUG.Println("Dispatch order=false copy goroutine exiting.")
						return
					}
				}
			}
		}
	}()
	go func() { // Main go routine handling inbound messages
		for message := range messages {
			h.RLock()
			m := messageFromPush(message, ackFunc(ackInChan, message))
			var handlers []MessageHandler
			for e := h.handlers.Front(); e != nil; e = e.Next() {

				hd := e.Value.(MessageHandler)
				wg.Add(1)
				go func() {
					hd(client, m)
					wg.Done()
				}()

			}
			h.RUnlock()
			for _, handler := range handlers {
				handler(client, m)

			}
			// DEBUG.Println(ROU, "matchAndDispatch handled message")
		}
		// Ensure that nothing further will be written to ackOutChan before closing it
		close(stopAckCopy)
		<-ackCopyStopped
		close(ackOutChan)
		go func() {
			wg.Wait() // Note: If this remains running then the user has payloadHandler that are not returning
			close(goRoutinesDone)
		}()

		DEBUG.Println("Dispatch exiting")
	}()
	return ackOutChan
}
func (c *client) UpdateLastReceived() {
	if c.options.Heartbeat != 0 {
		c.lastReceived.Store(time.Now())
	}
}

func (c *client) UpdateLastSent() {
	if c.options.Heartbeat != 0 {
		c.lastSent.Store(time.Now())
	}
}

func (c *client) getWriteTimeOut() time.Duration {
	return c.options.WriteTimeout
}
func (c *client) CloseConnect(reason byte) {

	switch reason {
	case 0:
		DEBUG.Println("Close connect normal")

	}
	DEBUG.Println("recv disconnect  ")
	c.Disconnect(100)
}
func (c *client) AddPayloadHandlerLast(handler MessageHandler) {
	c.payloadHandler.AddLast(handler)
}
func (c *client) internalConnLost(whyConnLost error) {
	// It is possible that internalConnLost will be called multiple times simultaneously
	// (including after sending a DisconnectPacket) as such we only do cleanup etc if the
	// routines were actually running and are not being disconnected at users request
	DEBUG.Println(CLI, "internalConnLost called")
	disDone, err := c.status.ConnectionLost(c.options.AutoReconnect && c.status.ConnectionStatus() > connecting)
	if err != nil {
		if err == errConnLossWhileDisconnecting || err == errAlreadyHandlingConnectionLoss {
			return // Loss of connection is expected or already being handled
		}
		ERROR.Println(CLI, fmt.Sprintf("internalConnLost unexpected status: %s", err.Error()))
		return
	}

	// c.stopCommsWorker returns a channel that is closed when the operation completes. This was required prior
	// to the implementation of proper status management but has been left in place, for now, to minimise change
	stopDone := c.stopCommsWorkers()
	// stopDone was required in previous versions because there was no connectionLost status (and there were
	// issues with status handling). This code has been left in place for the time being just in case the new
	// status handling contains bugs (refactoring required at some point).
	if stopDone == nil { // stopDone will be nil if workers already in the process of stopping or stopped
		ERROR.Println(CLI, "internalConnLost stopDone unexpectedly nil - BUG BUG")
		// Cannot really do anything other than leave things disconnected
		if _, err = disDone(false); err != nil { // Safest option - cannot leave status as connectionLost
			ERROR.Println(CLI, fmt.Sprintf("internalConnLost failed to set status to disconnected (stopDone): %s", err.Error()))
		}
		return
	}

	// It may take a while for the disconnection to complete whatever called us needs to exit cleanly so finnish in goRoutine
	go func() {
		DEBUG.Println(CLI, "internalConnLost waiting on workers")
		<-stopDone
		DEBUG.Println(CLI, "internalConnLost workers stopped")

		reConnDone, err := disDone(true)
		if err != nil {
			ERROR.Println(CLI, "failure whilst reporting completion of disconnect", err)
		} else if reConnDone == nil { // Should never happen
			ERROR.Println(CLI, "BUG BUG BUG reconnection function is nil", err)
		}

		reconnect := err == nil && reConnDone != nil

		if reconnect {
			go c.reconnect(reConnDone) // Will set connection status to reconnecting
		}
		if c.options.OnConnectionLost != nil {
			go c.options.OnConnectionLost(c, whyConnLost)
		}
		DEBUG.Println(CLI, "internalConnLost complete")
	}()
}
func (c *client) reconnect(connectionUp connCompletedFn) {
	DEBUG.Println(CLI, "enter reconnect")
	var (
		initSleep = 1 * time.Second
		conn      net.Conn
	)

	if slp, isContinual := c.backoff.sleepWithBackoff("connectionLost", initSleep, c.options.MaxReconnectInterval, 3*time.Second, true); isContinual {
		DEBUG.Println(CLI, "Detect continual connection lost after reconnect, slept for", int(slp.Seconds()), "seconds")
	}

	for {
		if nil != c.options.OnReconnecting {
			c.options.OnReconnecting(c, &c.options)
		}
		var err error
		conn, _, err = c.attemptConnection()
		if err == nil {
			break
		}
		sleep, _ := c.backoff.sleepWithBackoff("attemptReconnection", initSleep, c.options.MaxReconnectInterval, c.options.ConnectTimeout, false)
		DEBUG.Println(CLI, "Reconnect failed, slept for", int(sleep.Seconds()), "seconds:", err)

		if c.status.ConnectionStatus() != reconnecting { // Disconnect may have been called
			if err := connectionUp(false); err != nil { // Should always return an error
				ERROR.Println(CLI, err.Error())
			}
			DEBUG.Println(CLI, "Client moved to disconnected state while reconnecting, abandoning reconnect")
			return
		}
	}

	inboundFromStore := make(chan packets.ControlPacket)       // there may be some inbound comms packets in the store that are awaiting processing
	if !c.startWorkers(conn, connectionUp, inboundFromStore) { // note that this takes care of updating the status (to connected or disconnected)
		WARN.Println("Connect() called but connection established in another goroutine!")
	}
	close(inboundFromStore)
}
