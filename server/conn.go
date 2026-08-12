package server

import (
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/czqu/rmtt-go/codec"
)

// writeTimeout bounds how long a deviceConnection will block flushing a single
// packet to a client. A stuck or slow client must not pin the connection
// mutex: the keepalive reaper, session takeover, server shutdown and
// concurrent Push calls all acquire dc.mu, so an unbounded write deadlocks
// them. (Seen in production as a takeover SendDisconnect blocked on a client
// that never reads, which held dc.mu until the 10m test alarm fired while the
// keepalive reaper wedged on SendDisconnect.)
const writeTimeout = 5 * time.Second

// DeviceConnection is a single authenticated device connection.
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
	// Bound the write so a stalled client cannot pin dc.mu and deadlock the
	// keepalive reaper / takeover / shutdown paths. SetWriteDeadline is a
	// no-op on transports that don't support it (error ignored).
	_ = dc.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
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
	_ = dc.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	dp := codec.NewControlPacket(codec.Disconnect).(*codec.DisconnectPacket)
	dp.SetReturnCode(reason)
	_ = dp.Write(dc.conn)
}

func (dc *deviceConnection) Close() {
	if dc.active.CompareAndSwap(true, false) {
		dc.conn.Close()
	}
}
