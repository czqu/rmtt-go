package server

import (
	"errors"
	"net"
	"testing"
	"time"

	"github.com/czqu/rmtt-go/codec"
)

func TestDeviceConnection_Active(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()

	dc := newDeviceConnection(serverSide, "dev-001")
	if !dc.IsActive() {
		t.Error("IsActive() = false after create")
	}
	if dc.DeviceID() != "dev-001" {
		t.Errorf("DeviceID() = %q, want dev-001", dc.DeviceID())
	}
}

func TestDeviceConnection_Write(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()

	dc := newDeviceConnection(serverSide, "dev-001")
	errCh := make(chan error, 1)
	go func() {
		cp, err := codec.ReadPacket(clientSide)
		if err != nil {
			errCh <- err
			return
		}
		pp, ok := cp.(*codec.PushPacket)
		if !ok || string(pp.Payload) != "hello" {
			errCh <- errors.New("unexpected downlink packet")
			return
		}
		errCh <- nil
	}()

	if err := dc.Write([]byte("hello")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("downlink verification error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no PUSH packet received on the other end")
	}
}

func TestDeviceConnection_SendDisconnect(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()

	dc := newDeviceConnection(serverSide, "dev-001")
	rcCh := make(chan byte, 1)
	go func() {
		cp, err := codec.ReadPacket(clientSide)
		if err != nil {
			rcCh <- 0xFF
			return
		}
		dp, ok := cp.(*codec.DisconnectPacket)
		if !ok {
			rcCh <- 0xFF
			return
		}
		rcCh <- dp.GetReturnCode()
	}()

	dc.SendDisconnect(codec.DiscKickedByAdmin)
	select {
	case rc := <-rcCh:
		if rc != codec.DiscKickedByAdmin {
			t.Errorf("return code = 0x%x, want 0x%x", rc, codec.DiscKickedByAdmin)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no DISCONNECT packet received")
	}
}

func TestDeviceConnection_Close(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()

	dc := newDeviceConnection(serverSide, "dev-001")
	dc.Close()
	if dc.IsActive() {
		t.Error("IsActive() = true after Close")
	}
	if err := dc.Write([]byte("x")); !errors.Is(err, net.ErrClosed) {
		t.Errorf("Write() after Close = %v, want net.ErrClosed", err)
	}
}
