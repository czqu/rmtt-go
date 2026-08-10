package server

import (
	"net"
	"sync"
	"sync/atomic"

	"github.com/czqu/rmtt-go/codec"
)

type DeviceConnection interface {
	DeviceID() string
	IsActive() bool
	Write(payload []byte) error
	SendDisconnect(reason byte)
	Close()
}

type deviceConnection struct {
	mu       sync.Mutex
	conn     net.Conn
	deviceID string
	active   atomic.Bool
}

func newDeviceConnection(conn net.Conn, deviceID string) *deviceConnection {
	dc := &deviceConnection{
		conn:     conn,
		deviceID: deviceID,
	}
	dc.active.Store(true)
	return dc
}

func (dc *deviceConnection) DeviceID() string {
	return dc.deviceID
}

func (dc *deviceConnection) IsActive() bool {
	return dc.active.Load()
}

func (dc *deviceConnection) Write(payload []byte) error {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if !dc.active.Load() {
		return net.ErrClosed
	}
	pp := codec.NewControlPacket(codec.Push).(*codec.PushPacket)
	pp.Payload = payload
	return pp.Write(dc.conn)
}

func (dc *deviceConnection) SendDisconnect(reason byte) {
	dc.mu.Lock()
	defer dc.mu.Unlock()
	if !dc.active.Load() {
		return
	}
	dp := codec.NewControlPacket(codec.Disconnect).(*codec.DisconnectPacket)
	dp.SetReturnCode(reason)
	_ = dp.Write(dc.conn)
}

func (dc *deviceConnection) Close() {
	if dc.active.CompareAndSwap(true, false) {
		dc.conn.Close()
	}
}
