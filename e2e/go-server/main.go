// Command go-server is the Go side of the cross-language E2E matrix: a real
// rmtt-go server that Java / Rust clients connect to. Every upstream PUSH is
// logged as "PUSH_FROM_CLIENT device=<id> payload=<text>" and echoed back to
// the same device as "echo:<text>", which the remote-language client verifies.
//
// Prints GO_SERVER_READY port=<port> once the listener is bound.
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/czqu/rmtt-go/server"
)

// allowAll accepts any credential and uses it as the device id.
type allowAll struct{}

// Authenticate implements server.Authenticator.
func (allowAll) Authenticate(credential string) (string, bool) { return credential, true }

func main() {
	port := "19990"
	if len(os.Args) > 1 {
		port = os.Args[1]
	}

	var s server.Server
	opts := server.NewServerOptions()
	opts.AddListener(server.NewTCPListener(":" + port))
	opts.SetAuthenticator(allowAll{})
	opts.SetMessageHandler(func(deviceID string, payload []byte) {
		fmt.Printf("PUSH_FROM_CLIENT device=%s payload=%s\n", deviceID, payload)
		if s != nil {
			if err := s.Push(deviceID, []byte("echo:"+string(payload))); err != nil {
				fmt.Fprintln(os.Stderr, "echo push failed:", err)
			}
		}
	})
	s = server.NewServer(opts)

	fmt.Printf("GO_SERVER_READY port=%s\n", port)
	go func() {
		if err := s.ListenAndServe(); err != nil {
			fmt.Fprintln(os.Stderr, "listen error:", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	_ = s.Close()
}
