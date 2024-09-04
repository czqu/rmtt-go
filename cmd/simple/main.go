package main

import (
	"fmt"
	RMTT "github.com/czqu/rmtt-go"
	"log"
	"os"
	"time"
)

func main() {
	RMTT.DEBUG = log.New(os.Stdout, "", 0)
	RMTT.ERROR = log.New(os.Stdout, "", 0)
	RMTT.INFO = log.New(os.Stdout, "", 0)
	RMTT.WARN = log.New(os.Stdout, "", 0)

	opts := RMTT.NewClientOptions()

	opts.AddServer("tcp://127.0.0.1:3016")
	opts.AddServer("kcp://127.0.0.1:3017")
	opts.SetClientID("test-client")
	opts.SetConnectTimeout(5 * time.Second)
	opts.SetWriteTimeout(5 * time.Second)
	var callback RMTT.MessageHandler = func(client RMTT.Client, msg RMTT.Message) {
		fmt.Printf("recv msg size: %d\n", len(msg.Payload()))
	}

	c := RMTT.NewClient(opts)
	c.AddPayloadHandlerLast(callback)
	if token := c.Connect(); token.Wait() && token.Error() != nil {
		panic(token.Error())
	}

	i := 0
	for {

		time.Sleep(10 * time.Second)
		text := fmt.Sprintf("this is msg #%d!", i)
		i = i + 1
		fmt.Printf("send msg: %s\n", text)
		token := c.Push(text)

		token.Wait()
		if token.Error() != nil {
			fmt.Printf("token error: %s\n", token.Error())
			continue
		}

	}

}
