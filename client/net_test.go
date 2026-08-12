package client

import (
	"bytes"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/czqu/rmtt-go/codec"
	"github.com/quic-go/quic-go"
)

func TestQuicConf_Defaults(t *testing.T) {
	qc := quicConf(nil)
	if qc == nil {
		t.Fatal("quicConf(nil) = nil")
	}
	if qc.MaxIdleTimeout != 15*time.Minute {
		t.Errorf("MaxIdleTimeout = %v, want 15m", qc.MaxIdleTimeout)
	}
	if qc.KeepAlivePeriod != 30*time.Second {
		t.Errorf("KeepAlivePeriod = %v, want 30s", qc.KeepAlivePeriod)
	}
}

func TestQuicConf_Override(t *testing.T) {
	cfg := &quic.Config{MaxIdleTimeout: time.Minute, KeepAlivePeriod: 5 * time.Second}
	if got := quicConf(cfg); got != cfg {
		t.Error("quicConf(override) did not return the override")
	}
}

func TestOpenConnection_UnknownScheme(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:8080")
	_, err := openConnection(u, nil, &net.Dialer{}, nil)
	if err == nil {
		t.Error("openConnection() error = nil, want error")
	}
}

func TestVerifyCONNACK(t *testing.T) {
	ca := codec.NewControlPacket(codec.Connack).(*codec.ConnackPacket)
	ca.ReturnCode = codec.Accepted
	ca.ServerKeepalive = 30
	var buf bytes.Buffer
	if err := ca.Write(&buf); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	rc, kp, err := verifyCONNACK(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("verifyCONNACK() error = %v", err)
	}
	if rc != codec.Accepted {
		t.Errorf("rc = %d, want Accepted", rc)
	}
	if kp != 30 {
		t.Errorf("serverKp = %d, want 30", kp)
	}
}

func TestVerifyCONNACK_NonConnack(t *testing.T) {
	cp := codec.NewControlPacket(codec.Connect).(*codec.ConnectPacket)
	var buf bytes.Buffer
	if err := cp.Write(&buf); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if _, _, err := verifyCONNACK(bytes.NewReader(buf.Bytes())); err == nil {
		t.Error("verifyCONNACK() with non-CONNACK first packet error = nil, want error")
	}
}

func TestVerifyCONNACK_Truncated(t *testing.T) {
	if _, _, err := verifyCONNACK(bytes.NewReader([]byte{0x00})); err == nil {
		t.Error("verifyCONNACK() with truncated data error = nil, want error")
	}
}

func TestConnectServer(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	defer clientSide.Close()

	go func() {
		if _, err := codec.ReadPacket(serverSide); err != nil {
			return
		}
		ca := codec.NewControlPacket(codec.Connack).(*codec.ConnackPacket)
		ca.ReturnCode = codec.Accepted
		ca.ServerKeepalive = 60
		_ = ca.Write(serverSide)
	}()

	cm := codec.NewControlPacket(codec.Connect).(*codec.ConnectPacket)
	cm.Credential = "dev-001"
	rc, serverKp, err := connectServer(clientSide, cm, 1)
	if err != nil {
		t.Fatalf("connectServer() error = %v", err)
	}
	if rc != codec.Accepted {
		t.Errorf("rc = %d, want Accepted", rc)
	}
	if serverKp != 60 {
		t.Errorf("serverKp = %d, want 60", serverKp)
	}
}
