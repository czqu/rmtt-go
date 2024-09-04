package RMTT

import (
	"github.com/czqu/rmtt-go/packets"
	"io"
	"time"
)

func keepalive(c *client, conn io.Writer) {
	defer c.workers.Done()
	DEBUG.Println(CLI, "keepalive starting")

	checkInterval := time.Duration(c.options.Heartbeat) * time.Second / 4

	intervalTicker := time.NewTicker(checkInterval)
	defer intervalTicker.Stop()

	for {
		select {
		case <-c.stop:
			DEBUG.Println(CLI, "keepalive stopped")
			return
		case <-intervalTicker.C:
			lastSent := c.lastSent.Load().(time.Time)
			lastReceived := c.lastReceived.Load().(time.Time)

			DEBUG.Println(CLI, "heartbeat check", time.Since(lastSent).Seconds())
			if time.Since(lastSent) >= time.Duration(c.options.Heartbeat*int64(time.Second)) {

				ping := packets.NewControlPacket(packets.Pingreq).(*packets.PingreqPacket)
				DEBUG.Println(CLI, "keepalive sending ping ", time.Now())
				if err := ping.Write(conn); err != nil {
					ERROR.Println(err)
				}
				c.lastSent.Store(time.Now())

			}
			if time.Since(lastReceived) >= time.Duration(c.options.Heartbeat*int64(time.Second)*2) {
				WARN.Println(CLI, "receive time out")
				return
			}

		}
	}
}
