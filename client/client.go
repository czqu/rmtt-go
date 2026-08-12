package client

import (
	"bytes"
	"container/list"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/czqu/rmtt-go/codec"
)

// Client is a single connection to an rmtt server. Create one with NewClient;
// asynchronous operations are completed via Token.
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
	// connWriteMu serializes all writes to conn. The keepalive goroutine
	// writes PINGREQ directly to conn while startOutgoingComms writes
	// PUSH/DISCONNECT/PINGREQ via the obound channels; without serialization
	// a packet whose codec.Write issues multiple Write syscalls (bytes.Buffer
	// .WriteTo) could be interleaved with a keepalive PINGREQ, corrupting the
	// framed byte stream the peer is parsing.
	connWriteMu  sync.Mutex
	stop         chan struct{}
	lastSent     atomic.Value
	lastReceived atomic.Value
	serverKp     atomic.Int64
	workers      sync.WaitGroup
	obound       chan *PacketAndToken
	oboundP      chan *PacketAndToken
	commsStopped chan struct{}
	backoff      *backoffController
}

// NewClient creates a Client from the given options. The options value is
// copied, so later mutations have no effect on the client.
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
	t := newToken(codec.Connect).(*ConnectToken)
	connectionUp, err := c.status.Connecting()
	if err != nil {
		if err == errAlreadyConnectedOrReconnecting && c.options.AutoReconnect {
			WARN.Println("Connect() called but not disconnected")
			t.returnCode = codec.Accepted
			t.flowComplete()
			return t
		}
		ERROR.Println(err)
		t.setError(err)
		return t
	}
	go func() {
		if c.options.Servers == nil || len(c.options.Servers) == 0 {
			t.setError(fmt.Errorf("no server to connect to"))
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
				if c.options.ConnectRetry && !errors.Is(err, ProtocolViolationErr) && !errors.Is(err, RefusedNotAuthorisedErr) && !errors.Is(err, RefusedBadProtocolVersionErr) {
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

		inboundFromStore := make(chan codec.ControlPacket)
		if !c.startWorkers(conn, connectionUp, inboundFromStore) {
			WARN.Println(CLI, "Connect() called but connection established in another goroutine")
		}

		close(inboundFromStore)
		t.flowComplete()
		DEBUG.Println(CLI, "exit startClient")
	}()
	return t
}

func (c *client) startWorkers(conn net.Conn, connectionUp connCompletedFn, inboundFromStore <-chan codec.ControlPacket) bool {
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
	if c.options.Heartbeat != 0 || c.options.AdaptiveHeartbeat {
		c.lastReceived.Store(time.Now())
		c.lastSent.Store(time.Now())
		c.workers.Add(1)
		go keepalive(c, conn)
	}
	incomingPubChan := make(chan *codec.PushPacket)
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
					ackOut = nil
					c.workers.Done()
					continue
				}
				commsoboundP <- msg
			case <-c.stop:
				if ackOut != nil {
					for msg := range ackOut {
						commsoboundP <- msg
					}
					c.workers.Done()
				}
				close(commsoboundP)
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
					close(incomingPubChan)
					commsIncomingPub = nil
					continue
				}
			sendPubLoop:
				for {
					select {
					case incomingPubChan <- pub:
						break sendPubLoop
					case err, ok := <-commsErrors:
						if !ok {
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

func newConnectMsgFromOptions(options *ClientOptions, broker *url.URL) *codec.ConnectPacket {
	m := codec.NewControlPacket(codec.Connect).(*codec.ConnectPacket)
	m.MagicNumber = 0x637a7175
	m.ProtocolVersion = byte(options.ProtocolVersion)
	m.Credential = options.Credential
	kp := options.Heartbeat
	if options.AdaptiveHeartbeat {
		kp = options.AdaptiveMax
	}
	m.Keepalive = uint16(kp)
	return m
}

// Errors surfaced by Connect when the server rejects the CONNECT request
// (bad protocol version / not authorised) or a protocol violation is
// detected during the handshake.
var (
	RefusedNotAuthorisedErr      = errors.New("The server has rejected our request. Please check your permissions")
	RefusedBadProtocolVersionErr = errors.New("Server does not support protocol version")
	ProtocolViolationErr         = errors.New("The server has rejected our request. Please check your permissions")
	ErrDisconnectReceived        = errors.New("disconnect received from server")
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
		tlsCfg := c.options.TLSConfig
		conn, err = openConnection(server, tlsCfg, dialer, c.options.quicConfig)
		if c.options.OnConnectAttempt != nil {
			tlsCfg = c.options.OnConnectAttempt(server, c.options.TLSConfig)
		}
		if err != nil {
			ERROR.Println(err.Error())
			WARN.Println(CLI, "failed to connect to server, trying next, error:", err)
			rc = codec.ErrNetworkError
			continue
		}
		if err := conn.SetDeadline(connDeadline); err != nil {
			ERROR.Println("set deadline for handshake ", err)
		}
		var serverKp uint16
		rc, serverKp, err = connectServer(conn, cm, protocolVersion)
		if rc == codec.Accepted {
			if err := conn.SetDeadline(time.Time{}); err != nil {
				ERROR.Println("reset deadline following handshake ", err)
			}
			c.serverKp.Store(int64(serverKp))
			c.options.ProtocolVersion = protocolVersion
			INFO.Printf(CLI+"CONNACK accepted: server_kp=%d keepalive=%ds adaptive=%v",
				serverKp, c.options.Heartbeat, c.options.AdaptiveHeartbeat)
			break
		}
	}
	if rc == codec.ErrNetworkError {
		WARN.Println("Failed to connect to any server")
		return conn, rc, err
	}
	if rc == codec.Accepted {
		DEBUG.Println(CLI, "connected to server ", server)
		return conn, rc, err
	}
	if rc == codec.ErrRefusedBadProtocolVersion {
		ERROR.Println(CLI, "Server does not support protocol version")
		// The connection has never been handed to startWorkers (c.conn is still
		// nil), so c.Disconnect would be a no-op and leak the underlying conn.
		// Close it directly.
		_ = conn.Close()
		err = RefusedBadProtocolVersionErr
		return conn, rc, err
	}
	if rc == codec.ErrRefusedNotAuthorised {
		ERROR.Println(CLI, "The server has rejected our request. Please check your permissions")
		_ = conn.Close()
		err = RefusedNotAuthorisedErr
		return conn, rc, err
	}
	if rc == codec.ErrProtocolViolation {
		ERROR.Println(CLI, "Unsupported server protocol version ")
		_ = conn.Close()
		err = ProtocolViolationErr
		return conn, rc, err
	}

	return conn, rc, err
}

func (c *client) disconnect() {
	done := c.stopCommsWorkers()
	if done != nil {
		<-done
		DEBUG.Println(CLI, "forcefully disconnecting")
		DEBUG.Println(CLI, "disconnected")
	}
}

func (c *client) stopCommsWorkers() chan struct{} {
	DEBUG.Println(CLI, "stopCommsWorkers called")
	c.connMu.Lock()
	if c.conn == nil {
		DEBUG.Println(CLI, "stopCommsWorkers done (not running)")
		c.connMu.Unlock()
		return nil
	}

	close(c.stop)
	c.conn.Close()
	c.conn = nil
	c.connMu.Unlock()

	doneChan := make(chan struct{})

	go func() {
		DEBUG.Println(CLI, "stopCommsWorkers waiting for workers")
		c.workers.Wait()
		DEBUG.Println(CLI, "stopCommsWorkers waiting for comms")
		<-c.commsStopped
		DEBUG.Println(CLI, "stopCommsWorkers done")
		close(doneChan)
	}()
	return doneChan
}

func (c *client) Disconnect(quiesce uint) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		disDone, err := c.status.Disconnecting()
		if err != nil {
			WARN.Println(CLI, err.Error())
			return
		}
		defer func() {
			c.disconnect()
			disDone()
		}()
		DEBUG.Println(CLI, "disconnecting")
		dm := codec.NewControlPacket(codec.Disconnect).(*codec.DisconnectPacket)
		dt := newToken(codec.Disconnect)
		select {
		case c.oboundP <- &PacketAndToken{p: dm, t: dt}:
			DEBUG.Println(CLI, "calling WaitTimeout")
			dt.WaitTimeout(time.Duration(quiesce) * time.Millisecond)
			DEBUG.Println(CLI, "WaitTimeout done")
		case <-time.After(time.Duration(quiesce) * time.Millisecond):
			WARN.Println("Disconnect packet not sent due to timeout")
		}
	}()

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
		return s == reconnecting || (s == disconnecting && r)
	default:
		return false
	}
}

// ErrNotConnected is returned by Push when the client is not connected.
var ErrNotConnected = errors.New("not Connected")

func (c *client) Push(payload interface{}) Token {
	token := newToken(codec.Push).(*PushToken)
	DEBUG.Println("enter Push")
	switch {
	case !c.IsConnected():
		token.setError(ErrNotConnected)
		return token
	case c.status.ConnectionStatus() == reconnecting:
		token.flowComplete()
		return token
	}
	pub := codec.NewControlPacket(codec.Push).(*codec.PushPacket)
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

// MessageHandler receives a PUSH message from the server.
type MessageHandler func(Client, Message)

type handler struct {
	sync.RWMutex
	handlers *list.List
	messages chan *codec.PushPacket
}

func newHandler() *handler {
	router := &handler{handlers: list.New(), messages: make(chan *codec.PushPacket)}
	return router
}

func (h *handler) AddLast(handler MessageHandler) {
	h.Lock()
	defer h.Unlock()
	h.handlers.PushBack(handler)
}

func (h *handler) dispatch(messages <-chan *codec.PushPacket, client *client) <-chan *PacketAndToken {
	var wg sync.WaitGroup
	ackOutChan := make(chan *PacketAndToken)
	var ackInChan chan *PacketAndToken
	stopAckCopy := make(chan struct{})
	ackCopyStopped := make(chan struct{})
	goRoutinesDone := make(chan struct{})
	ackInChan = make(chan *PacketAndToken)
	go func() {
		for {
			select {
			case a := <-ackInChan:
				ackOutChan <- a
			case <-stopAckCopy:
				close(ackCopyStopped)
				for {
					select {
					case <-ackInChan:
						DEBUG.Println("Dispatch received acknowledgment after processing stopped (ACK dropped).")
					case <-goRoutinesDone:
						close(ackInChan)
						DEBUG.Println("Dispatch order=false copy goroutine exiting.")
						return
					}
				}
			}
		}
	}()
	go func() {
		for message := range messages {
			h.RLock()
			m := messageFromPush(message, ackFunc(ackInChan, message))
			for e := h.handlers.Front(); e != nil; e = e.Next() {
				hd := e.Value.(MessageHandler)
				wg.Add(1)
				go func() {
					hd(client, m)
					wg.Done()
				}()
			}
			h.RUnlock()
		}
		close(stopAckCopy)
		<-ackCopyStopped
		close(ackOutChan)
		go func() {
			wg.Wait()
			close(goRoutinesDone)
		}()
		DEBUG.Println("Dispatch exiting")
	}()
	return ackOutChan
}

func (c *client) UpdateLastReceived() {
	if c.options.Heartbeat != 0 || c.options.AdaptiveHeartbeat {
		c.lastReceived.Store(time.Now())
	}
}

func (c *client) UpdateLastSent() {
	if c.options.Heartbeat != 0 || c.options.AdaptiveHeartbeat {
		c.lastSent.Store(time.Now())
	}
}

func (c *client) getWriteTimeOut() time.Duration {
	return c.options.WriteTimeout
}

func (c *client) CloseConnect(reason byte) {
	DEBUG.Println("recv disconnect reason", reason)
	switch reason {
	case codec.DiscNormalDisconnect:
		c.Disconnect(100)
	case codec.DiscSessionTakenOver, codec.DiscKickedByAdmin:
		c.Disconnect(100)
	default:
		c.internalConnLost(ErrDisconnectReceived)
	}
}

func (c *client) AddPayloadHandlerLast(handler MessageHandler) {
	c.payloadHandler.AddLast(handler)
}

func (c *client) internalConnLost(whyConnLost error) {
	DEBUG.Println(CLI, "internalConnLost called")
	disDone, err := c.status.ConnectionLost(c.options.AutoReconnect && c.status.ConnectionStatus() > connecting)
	if err != nil {
		if err == errConnLossWhileDisconnecting || err == errAlreadyHandlingConnectionLoss {
			return
		}
		ERROR.Println(CLI, fmt.Sprintf("internalConnLost unexpected status: %s", err.Error()))
		return
	}

	stopDone := c.stopCommsWorkers()
	if stopDone == nil {
		ERROR.Println(CLI, "internalConnLost stopDone unexpectedly nil - BUG BUG")
		if _, err = disDone(false); err != nil {
			ERROR.Println(CLI, fmt.Sprintf("internalConnLost failed to set status to disconnected (stopDone): %s", err.Error()))
		}
		return
	}

	go func() {
		DEBUG.Println(CLI, "internalConnLost waiting on workers")
		<-stopDone
		DEBUG.Println(CLI, "internalConnLost workers stopped")

		reConnDone, err := disDone(true)
		if err != nil {
			ERROR.Println(CLI, "failure whilst reporting completion of disconnect", err)
		} else if reConnDone == nil {
			ERROR.Println(CLI, "BUG BUG BUG reconnection function is nil", err)
		}

		reconnect := err == nil && reConnDone != nil

		if reconnect {
			go c.reconnect(reConnDone)
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
		initSleep = c.options.ReconnectBase
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

		if c.status.ConnectionStatus() != reconnecting {
			if err := connectionUp(false); err != nil {
				ERROR.Println(CLI, err.Error())
			}
			DEBUG.Println(CLI, "Client moved to disconnected state while reconnecting, abandoning reconnect")
			return
		}
	}

	inboundFromStore := make(chan codec.ControlPacket)
	if !c.startWorkers(conn, connectionUp, inboundFromStore) {
		WARN.Println("Connect() called but connection established in another goroutine!")
	}
	close(inboundFromStore)
}
