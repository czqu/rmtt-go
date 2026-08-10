package main

import (
	"fmt"
	"log"
	"os"

	"github.com/czqu/rmtt-go/server"
)

func main() {
	log.SetOutput(os.Stdout)

	opts := server.NewServerOptions()
	opts.SetPort(18883)
	opts.SetAuthenticator(&simpleAuth{})
	opts.SetMessageHandler(func(deviceID string, payload []byte) {
		fmt.Printf("device %s sent: %s\n", deviceID, string(payload))
	})
	opts.SetConnectionListener(&simpleListener{})

	srv := server.NewServer(opts)
	log.Println("starting server on :18883")
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

type simpleAuth struct{}

func (a *simpleAuth) Authenticate(credential string) (string, bool) {
	fmt.Printf("auth: credential=%s\n", credential)
	return credential, true
}

type simpleListener struct{}

func (l *simpleListener) OnConnectionEstablished(deviceID string) {
	fmt.Printf("device connected: %s\n", deviceID)
}

func (l *simpleListener) OnConnectionClosed(deviceID string, reason string) {
	fmt.Printf("device disconnected: %s (reason: %s)\n", deviceID, reason)
}
