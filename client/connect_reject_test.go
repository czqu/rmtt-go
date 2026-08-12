package client

import (
	"bufio"
	"errors"
	"io"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/czqu/rmtt-go/codec"
)

// startRejectServer listens on a random TCP port, reads one CONNECT, and replies
// with a CONNACK carrying the given return code. It then blocks reading until
// the client closes the conn. Returns the server URL and a function that
// reports the terminal read error: io.EOF means the client closed the conn
// (good); a timeout means it leaked (bad).
func startRejectServer(t *testing.T, rc byte) (*url.URL, func() error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	// readResult receives exactly one value: the error from the blocking read
	// after the client closes (io.EOF on a clean close). It is NOT pre-seeded
	// so the waiter cannot race ahead of the server goroutine and misread an
	// "open" state.
	readResult := make(chan error, 1)

	go func() {
		defer ln.Close()
		conn, err := ln.Accept()
		if err != nil {
			readResult <- err
			return
		}
		// Bound how long we wait for the client to close: shorter than the
		// test's overall wait so a leak is reported as a timeout, not a hang.
		_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		// Read the CONNECT so the handshake progresses past the write.
		if _, err := codec.ReadPacket(conn); err != nil {
			readResult <- err
			conn.Close()
			return
		}
		ca := codec.NewControlPacket(codec.Connack).(*codec.ConnackPacket)
		ca.ReturnCode = rc
		ca.ServerKeepalive = 60
		if err := ca.Write(conn); err != nil {
			readResult <- err
			conn.Close()
			return
		}
		// Keep reading until the client closes (EOF) or the deadline. This lets
		// the test observe close-on-reject.
		br := bufio.NewReader(conn)
		_, rerr := br.ReadByte()
		readResult <- rerr
		conn.Close()
	}()

	u, _ := url.Parse("tcp://" + ln.Addr().String())
	return u, func() error {
		select {
		case e := <-readResult:
			return e
		case <-time.After(3 * time.Second):
			return errors.New("timeout waiting for client to close conn")
		}
	}
}

// TestConnect_RejectedClosesConn verifies that when the server rejects the
// CONNECT (bad protocol version / not authorised), the client returns the right
// error AND closes the underlying connection — previously it called
// c.Disconnect(100) which was a no-op (c.conn still nil) and leaked the conn.
func TestConnect_RejectedClosesConn(t *testing.T) {
	tests := []struct {
		name    string
		rc      byte
		wantErr error
	}{
		{"bad protocol version", codec.ErrRefusedBadProtocolVersion, RefusedBadProtocolVersionErr},
		{"not authorised", codec.ErrRefusedNotAuthorised, RefusedNotAuthorisedErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srvURL, connState := startRejectServer(t, tt.rc)

			o := NewClientOptions()
			o.Servers = []*url.URL{srvURL}
			o.AutoReconnect = false // fail fast, don't retry
			o.ConnectRetry = false
			o.ConnectTimeout = 2 * time.Second
			c := NewClient(o)

			tok := c.Connect()
			if !tok.WaitTimeout(5 * time.Second) {
				t.Fatal("Connect() token did not complete")
			}
			if !errors.Is(tok.Error(), tt.wantErr) {
				t.Fatalf("Connect() error = %v, want %v", tok.Error(), tt.wantErr)
			}

			// The client must have closed the conn so the server's blocking
			// read returns EOF (not a timeout, and not a forever-open conn).
			got := connState()
			if got == nil {
				t.Fatal("server conn still open after rejected CONNECT; client leaked the connection")
			}
			if !errors.Is(got, io.EOF) {
				// Some platforms report "use of closed network connection";
				// both indicate the peer closed. Anything but nil/timeout is OK.
				t.Logf("client closed conn (server read: %v)", got)
			}
		})
	}
}
