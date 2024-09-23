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
	"crypto/tls"
	"errors"
	"github.com/czqu/rmtt-go/packets"
	"github.com/xtaci/kcp-go"
	"io"
	"net"
	"reflect"
	"strings"
	"sync"
	"time"

	"net/url"
)

const closedNetConnErrorText = "use of closed network connection"

func openConnection(uri *url.URL, tlsc *tls.Config, dialer *net.Dialer) (net.Conn, error) {
	switch uri.Scheme {
	case "tcp":
		conn, err := dialer.Dial("tcp", uri.Host)
		if err != nil {
			return nil, err
		}
		return conn, nil
	case "kcp":
		kcpConn, err := kcp.Dial(uri.Host)
		if err != nil {
			return nil, err
		}
		return kcpConn, nil
	case "tls":
		conn, err := dialer.Dial("tcp", uri.Host)
		if err != nil {
			return nil, err
		}

		tlsConn := tls.Client(conn, tlsc)

		err = tlsConn.Handshake()
		if err != nil {
			_ = conn.Close()
			return nil, err
		}

		return tlsConn, nil

	}
	return nil, errors.New("unknown protocol")
}
func connectServer(conn io.ReadWriter, cm *packets.ConnectPacket, protocolVersion uint) (byte, error) {

	DEBUG.Println(NET, "connect started")
	if err := cm.Write(conn); err != nil {
		ERROR.Println(err)
		return packets.ErrNetworkError, err
	}

	rc, err := verifyCONNACK(conn)
	return rc, err
}
func verifyCONNACK(conn io.Reader) (byte, error) {
	DEBUG.Println(NET, "connect started")

	ca, err := packets.ReadPacket(conn)
	if err != nil {
		ERROR.Println(NET, "connect got error", err)
		return packets.ErrNetworkError, err
	}

	if ca == nil {
		ERROR.Println(NET, "received nil packet")
		return packets.ErrNetworkError, errors.New("nil CONNACK packet")
	}

	msg, ok := ca.(*packets.ConnackPacket)
	if !ok {
		ERROR.Println(NET, "received msg that was not CONNACK")
		return packets.ErrNetworkError, errors.New("non-CONNACK first packet received")
	}

	DEBUG.Println(NET, "received connack")
	return msg.ReturnCode, nil
}
func ackFunc(oboundP chan *PacketAndToken, packet *packets.PushPacket) func() {
	return func() {

		// do nothing, since there is no need to send an ack packet back
	}

}

type commsFns interface {
	UpdateLastReceived()            // Must be called whenever a packet is received
	UpdateLastSent()                // Must be called whenever a packet is successfully sent
	getWriteTimeOut() time.Duration // Return the writetimeout (or 0 if none)
	CloseConnect(reason byte)
}

func startComms(conn net.Conn,
	c commsFns,
	inboundFromStore <-chan packets.ControlPacket,
	oboundp <-chan *PacketAndToken,
	obound <-chan *PacketAndToken) (
	<-chan *packets.PushPacket,
	<-chan error,
) {

	ibound := startIncomingComms(conn, c, inboundFromStore)
	outboundFromIncoming := make(chan *PacketAndToken)

	oboundErr := startOutgoingComms(conn, c, oboundp, obound, outboundFromIncoming)
	DEBUG.Println(NET, "startComms started")

	var wg sync.WaitGroup
	wg.Add(2)

	outPublish := make(chan *packets.PushPacket)
	outError := make(chan error)

	// Any messages received get passed to the appropriate channel
	go func() {
		for ic := range ibound {
			if ic.err != nil {
				outError <- ic.err
				continue
			}
			if ic.outbound != nil {
				outboundFromIncoming <- ic.outbound
				continue
			}
			if ic.incomingPub != nil {
				outPublish <- ic.incomingPub
				continue
			}
			ERROR.Println("startComms received empty incomingComms msg")
		}
		// Close channels that will not be written to again (allowing other routines to exit)
		close(outboundFromIncoming)
		close(outPublish)
		wg.Done()
	}()

	// Any errors will be passed out to our caller
	go func() {
		for err := range oboundErr {
			outError <- err
		}
		wg.Done()
	}()

	// outError is used by both routines so can only be closed when they are both complete
	go func() {
		wg.Wait()
		close(outError)
		DEBUG.Println(NET, "startComms closing outError")
	}()

	return outPublish, outError
}

type incomingComms struct {
	err         error               // If non-nil then there has been an error (ignore everything else)
	outbound    *PacketAndToken     // Packet (with token) than needs to be sent out (e.g. an acknowledgement)
	incomingPub *packets.PushPacket // A new publish has been received; this will need to be passed on to our user
}
type inbound struct {
	err error
	cp  packets.ControlPacket
}

func startIncoming(conn io.Reader) <-chan inbound {
	var err error
	var cp packets.ControlPacket
	ibound := make(chan inbound)

	DEBUG.Println(NET, "incoming started")

	go func() {
		for {
			if cp, err = packets.ReadPacket(conn); err != nil {
				// We do not want to log the error if it is due to the network connection having been closed
				// elsewhere (i.e. after sending DisconnectPacket). Detecting this situation is the subject of
				// https://github.com/golang/go/issues/4373
				if !strings.Contains(err.Error(), closedNetConnErrorText) {
					ibound <- inbound{err: err}
				}
				close(ibound)
				DEBUG.Println(NET, "incoming complete")
				return
			}
			DEBUG.Println(NET, "startIncoming Received Message")
			ibound <- inbound{cp: cp}
		}
	}()

	return ibound
}
func startIncomingComms(conn io.Reader,
	c commsFns,
	inboundFromStore <-chan packets.ControlPacket,
) <-chan incomingComms {
	ibound := startIncoming(conn) // Start goroutine that reads from network connection
	output := make(chan incomingComms)

	DEBUG.Println(NET, "startIncomingComms started")
	go func() {
		for {
			if inboundFromStore == nil && ibound == nil {
				close(output)
				DEBUG.Println(NET, "startIncomingComms goroutine complete")
				return // As soon as ibound is closed we can exit (should have already processed an error)
			}
			DEBUG.Println(NET, "logic waiting for msg on ibound")

			var msg packets.ControlPacket
			var ok bool
			select {
			case msg, ok = <-inboundFromStore:
				if !ok {
					DEBUG.Println(NET, "startIncomingComms: inboundFromStore complete")
					inboundFromStore = nil // should happen quickly as this is only for persisted messages
					continue
				}
				DEBUG.Println(NET, "startIncomingComms: got msg from store")
			case ibMsg, ok := <-ibound:
				if !ok {
					DEBUG.Println(NET, "startIncomingComms: ibound complete")
					ibound = nil
					continue
				}
				DEBUG.Println(NET, "startIncomingComms: got msg on ibound")
				// If the inbound comms routine encounters any issues it will send us an error.
				if ibMsg.err != nil {
					output <- incomingComms{err: ibMsg.err}
					continue // Usually the channel will be closed immediately after sending an error but safer that we do not assume this
				}
				msg = ibMsg.cp

				c.UpdateLastReceived() // Notify keepalive logic that we recently received a packet
			}

			switch m := msg.(type) {

			case *packets.PushPacket:
				DEBUG.Println(NET, "startIncomingComms: received publish")
				output <- incomingComms{incomingPub: m}
			case *packets.PingreqPacket:
				DEBUG.Println(NET, "receive heartbeat")
			case *packets.PingrespPacket:
				DEBUG.Println("receive heartbeat ", time.Now())
			case *packets.DisconnectPacket:
				closeMsg, _ := msg.(*packets.DisconnectPacket)
				c.CloseConnect(closeMsg.GetReturnCode())
			}
		}
	}()
	return output
}

func startOutgoingComms(conn net.Conn,
	c commsFns,
	oboundp <-chan *PacketAndToken,
	obound <-chan *PacketAndToken,
	oboundFromIncoming <-chan *PacketAndToken,
) <-chan error {
	errChan := make(chan error)
	DEBUG.Println(NET, "outgoing started")

	go func() {
		for {
			DEBUG.Println(NET, "outgoing waiting for an outbound message")

			// This goroutine will only exits when all of the input channels we receive on have been closed. This approach is taken to avoid any
			// deadlocks (if the connection goes down there are limited options as to what we can do with anything waiting on us and
			// throwing away the packets seems the best option)
			if oboundp == nil && obound == nil && oboundFromIncoming == nil {
				DEBUG.Println(NET, "outgoing comms stopping")
				close(errChan)
				return
			}

			select {
			case pub, ok := <-obound:
				if !ok {
					obound = nil
					continue
				}
				msg := pub.p.(*packets.PushPacket)
				DEBUG.Println(NET, "obound msg to write")

				writeTimeout := c.getWriteTimeOut()
				if writeTimeout > 0 {
					if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
						ERROR.Println(NET, "SetWriteDeadline ", err)
					}
				}

				if err := msg.Write(conn); err != nil {
					ERROR.Println(NET, "outgoing obound reporting error ", err)
					pub.t.setError(err)

					if !strings.Contains(err.Error(), closedNetConnErrorText) {
						errChan <- err
					}
					continue
				}

				if writeTimeout > 0 {

					if err := conn.SetWriteDeadline(time.Time{}); err != nil {
						ERROR.Println(NET, "SetWriteDeadline to 0 ", err)
					}
				}

				pub.t.flowComplete()

				DEBUG.Println(NET, "obound wrote msg")
			case msg, ok := <-oboundp:
				if !ok {
					oboundp = nil
					continue
				}
				DEBUG.Println(NET, "obound priority msg to write, type", reflect.TypeOf(msg.p))
				if err := msg.p.Write(conn); err != nil {
					ERROR.Println(NET, "outgoing oboundp reporting error ", err)
					if msg.t != nil {
						msg.t.setError(err)
					}
					errChan <- err
					continue
				}

				if _, ok := msg.p.(*packets.DisconnectPacket); ok {
					msg.t.(*DisconnectToken).flowComplete()
					DEBUG.Println(NET, "outbound wrote disconnect, closing connection")
					_ = conn.Close()
				}
			case msg, ok := <-oboundFromIncoming:
				if !ok {
					oboundFromIncoming = nil
					continue
				}
				DEBUG.Println(NET, "obound from incoming msg to write, type", reflect.TypeOf(msg.p))
				if err := msg.p.Write(conn); err != nil {
					ERROR.Println(NET, "outgoing oboundFromIncoming reporting error", err)
					if msg.t != nil {
						msg.t.setError(err)
					}
					errChan <- err
					continue
				}
			}
			c.UpdateLastSent()
		}
	}()
	return errChan
}
