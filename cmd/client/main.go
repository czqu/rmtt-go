package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/czqu/rmtt-go/client"
)

// buildOptions assembles the client configuration used by the sample.
func buildOptions(addr, credential string) *client.ClientOptions {
	opts := client.NewClientOptions()
	opts.AddServer(addr)
	opts.SetCredential(credential)
	opts.SetConnectTimeout(5 * time.Second)
	opts.SetWriteTimeout(5 * time.Second)
	opts.AutoReconnect = true
	return opts
}

func main() {
	client.DEBUG = log.New(os.Stdout, "[DEBUG] ", 0)
	client.ERROR = log.New(os.Stdout, "[ERROR] ", 0)
	client.INFO = log.New(os.Stdout, "[INFO] ", 0)
	client.WARN = log.New(os.Stdout, "[WARN] ", 0)

	opts := buildOptions("tcp://127.0.0.1:18883", "test-device")

	c := client.NewClient(opts)
	c.AddPayloadHandlerLast(func(cl client.Client, msg client.Message) {
		fmt.Printf("received: %s\n", string(msg.Payload()))
	})

	if token := c.Connect(); token.Wait() && token.Error() != nil {
		log.Fatalf("connect failed: %v", token.Error())
	}
	fmt.Println("connected")

	i := 0
	for {
		time.Sleep(10 * time.Second)
		text := fmt.Sprintf("hello #%d", i)
		i++
		fmt.Printf("sending: %s\n", text)
		token := c.Push(text)
		token.Wait()
		if token.Error() != nil {
			fmt.Printf("push error: %v\n", token.Error())
		}
	}
}
