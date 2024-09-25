package main

import (
	"crypto/tls"
	"fmt"
	RMTT "github.com/czqu/rmtt-go"
	"log"
	"net/url"
	"os"
	"time"
)

func main() {
	RMTT.DEBUG = log.New(os.Stdout, "", 0)
	RMTT.ERROR = log.New(os.Stdout, "", 0)
	RMTT.INFO = log.New(os.Stdout, "", 0)
	RMTT.WARN = log.New(os.Stdout, "", 0)

	opts := RMTT.NewClientOptions()

	tlsConfig := &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS13,
		NextProtos:         []string{"h3"},
	}
	opts.SetTlsConfig(tlsConfig)
	opts.AddServer("quic://127.0.0.1:3019")
	opts.AddServer("tls://127.0.0.1:3018")
	opts.AddServer("tcp://127.0.0.1:3016")
	opts.AddServer("kcp://127.0.0.1:3017")

	opts.SetToken("test")
	opts.SetConnectTimeout(5 * time.Second)
	opts.SetWriteTimeout(5 * time.Second)
	opts.SetConnectTimeout(5 * time.Second)
	opts.AutoReconnect = true
	opts.ConnectRetryInterval = 1 * time.Second
	opts.SetConnectionAttemptHandler(
		func(server *url.URL, tlsCfg *tls.Config) *tls.Config {
			fmt.Printf("Connecting to %s\n", server.String())
			return tlsConfig
		})
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
