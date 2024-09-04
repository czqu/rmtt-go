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
	"net/url"
	"strings"
	"time"
)

type ConnectionLostHandler func(Client, error)
type ReconnectHandler func(Client, *ClientOptions)
type ClientOptions struct {
	Servers              []*url.URL
	ClientID             string
	Heartbeat            int64
	ProtocolVersion      uint
	ConnectRetry         bool
	ConnectRetryInterval time.Duration
	ConnectTimeout       time.Duration
	WriteTimeout         time.Duration
	AutoReconnect        bool
	OnConnectionLost     ConnectionLostHandler
	MaxReconnectInterval time.Duration
	OnReconnecting       ReconnectHandler
}

func (o *ClientOptions) AddServer(server string) *ClientOptions {
	if len(server) > 0 && server[0] == ':' {
		server = "127.0.0.1" + server
	}
	if !strings.Contains(server, "://") {
		server = "tcp://" + server
	}
	serverURI, err := url.Parse(server)
	if err != nil {
		ERROR.Println("Failed to parse address: %s", server, err)
		return o
	}
	o.Servers = append(o.Servers, serverURI)
	return o
}
func (o *ClientOptions) SetClientID(id string) *ClientOptions {
	o.ClientID = id
	return o
}
func (o *ClientOptions) SetHeartbeat(k time.Duration) *ClientOptions {
	o.Heartbeat = int64(k / time.Second)
	return o
}
func (o *ClientOptions) SetConnectTimeout(k time.Duration) *ClientOptions {
	o.ConnectTimeout = k
	return o
}
func (o *ClientOptions) SetWriteTimeout(k time.Duration) *ClientOptions {
	o.WriteTimeout = k
	return o
}
func NewClientOptions() *ClientOptions {
	o := &ClientOptions{
		Servers:              nil,
		ClientID:             "",
		Heartbeat:            10,
		ProtocolVersion:      0,
		ConnectRetry:         true,
		ConnectRetryInterval: 30 * time.Second,
		ConnectTimeout:       30 * time.Second,
		AutoReconnect:        true,
		OnConnectionLost:     nil,
		MaxReconnectInterval: 10 * time.Minute,
		OnReconnecting:       nil,
	}
	return o
}
