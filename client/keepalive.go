package client

import (
	"errors"
	"io"
	"time"

	"github.com/czqu/rmtt-go/codec"
)

var errReceiveTimeout = errors.New("keepalive receive timeout")

// writePingLocked serializes the PINGREQ write with startOutgoingComms via
// connWriteMu so the framed stream cannot be corrupted by interleaved writes
// (see the note on client.connWriteMu). conn is io.Writer here; the mutex
// lives on the *client, so callers pass c in.
func writePingReq(c *client, conn io.Writer, ping *codec.PingreqPacket) error {
	c.connWriteMu.Lock()
	defer c.connWriteMu.Unlock()
	return ping.Write(conn)
}

func (c *client) effectiveHeartbeat() int64 {
	if kp := c.serverKp.Load(); kp > 0 {
		return kp
	}
	return c.options.Heartbeat
}

func keepalive(c *client, conn io.Writer) {
	defer c.workers.Done()
	DEBUG.Println(CLI, "keepalive starting")

	if c.options.AdaptiveHeartbeat {
		adaptiveKeepalive(c, conn)
		return
	}

	heartbeat := c.effectiveHeartbeat()
	if heartbeat <= 0 {
		DEBUG.Println(CLI, "keepalive disabled (server_kp==0)")
		return
	}
	INFO.Printf(CLI+"keepalive: heartbeat=%ds server_kp=%d",
		heartbeat, c.serverKp.Load())

	checkInterval := time.Duration(heartbeat) * time.Second / 4
	if checkInterval < time.Second {
		checkInterval = time.Second
	}

	intervalTicker := time.NewTicker(checkInterval)
	defer intervalTicker.Stop()

	for {
		select {
		case <-c.stop:
			DEBUG.Println(CLI, "keepalive stopped")
			return
		case <-intervalTicker.C:
			heartbeat = c.effectiveHeartbeat()
			if heartbeat <= 0 {
				continue
			}
			lastSent := c.lastSent.Load().(time.Time)
			lastReceived := c.lastReceived.Load().(time.Time)

			DEBUG.Println(CLI, "heartbeat check", time.Since(lastSent).Seconds())
			if time.Since(lastSent) >= time.Duration(heartbeat*int64(time.Second)) {
				ping := codec.NewControlPacket(codec.Pingreq).(*codec.PingreqPacket)
				DEBUG.Println(CLI, "keepalive sending ping ", time.Now())
				if err := writePingReq(c, conn, ping); err != nil {
					ERROR.Println(err)
				}
				c.lastSent.Store(time.Now())
			}
			if time.Since(lastReceived) >= time.Duration(float64(heartbeat)*float64(time.Second)*1.5) {
				WARN.Println(CLI, "receive time out")
				c.internalConnLost(errReceiveTimeout)
				return
			}
		}
	}
}

// adaptivePhase mirrors the Java AdaptiveHeartbeat state machine.
type adaptivePhase int

const (
	phaseProbeShort adaptivePhase = iota
	phaseProbeDoubling
	phaseProbeFine
	phaseStable
)

const adaptiveTickInterval = 250 * time.Millisecond

// adaptiveKeepalive probes the maximum sustainable heartbeat interval within
// [shortSeconds, min(maxSeconds, server_kp)] and settles at ~90% of the found maximum.
// A lost heartbeat in the stable state falls back to the short period and re-adapts; a probe
// failure during the short-liveness phase escalates to a connection loss.
func adaptiveKeepalive(c *client, conn io.Writer) {
	shortMillis := int64(c.options.AdaptiveShort) * 1000
	if shortMillis < 1000 {
		shortMillis = 1000
	}
	maxMillis := int64(c.options.AdaptiveMax) * 1000
	if maxMillis < shortMillis {
		maxMillis = shortMillis
	}
	kp := c.serverKp.Load()
	ceilingMillis := maxMillis
	if kp > 0 {
		if kpMs := kp * 1000; kpMs < ceilingMillis {
			ceilingMillis = kpMs
		}
	}
	if kp <= 0 {
		// server disabled keepalive (server_kp==0): no PING at all
		INFO.Println(CLI, "adaptive heartbeat: server_kp==0, keepalive disabled")
		return
	}
	if ceilingMillis <= shortMillis {
		// no adaptive headroom: keep the server_kp cadence only
		INFO.Println(CLI, "adaptive heartbeat: no room above short period, using server_kp", kp)
		fixedKeepalive(c, conn)
		return
	}

	probeCount := c.options.ProbeCount
	if probeCount < 1 {
		probeCount = 3
	}
	responseWindow := c.options.ResponseWindow
	if responseWindow <= 0 {
		responseWindow = 2 * time.Second
	}
	fineStepMillis := int64(c.options.FineStep) * 1000
	if fineStepMillis < 1000 {
		fineStepMillis = 1000
	}

	phase := phaseProbeShort
	shortOk := 0
	intervalMillis := shortMillis
	var lastSuccessMillis int64
	var successHeartMillis int64
	var sentAt time.Time
	awaiting := false

	INFO.Printf(CLI+"adaptive heartbeat started: short=%ds max=%ds ceiling=%ds probeCount=%d",
		shortMillis/1000, maxMillis/1000, ceilingMillis/1000, probeCount)

	ticker := time.NewTicker(adaptiveTickInterval)
	defer ticker.Stop()

	gotResponse := func() bool {
		return c.lastReceived.Load().(time.Time).After(sentAt) ||
			c.lastReceived.Load().(time.Time).Equal(sentAt)
	}

	enterStable := func() {
		if lastSuccessMillis > 0 {
			successHeartMillis = lastSuccessMillis * 9 / 10
			if successHeartMillis < shortMillis {
				successHeartMillis = shortMillis
			}
		} else {
			successHeartMillis = shortMillis
		}
		phase = phaseStable
		sentAt = time.Time{}
		awaiting = false
		INFO.Printf(CLI+"adaptive heartbeat stable at %ds (last success %ds)",
			successHeartMillis/1000, lastSuccessMillis/1000)
	}

	sendProbe := func() {
		ping := codec.NewControlPacket(codec.Pingreq).(*codec.PingreqPacket)
		if err := writePingReq(c, conn, ping); err != nil {
			ERROR.Println(CLI, "adaptive heartbeat ping write error:", err)
		}
		c.lastSent.Store(time.Now())
		sentAt = time.Now()
		awaiting = true
	}

	for {
		select {
		case <-c.stop:
			DEBUG.Println(CLI, "adaptive keepalive stopped")
			return
		case now := <-ticker.C:
			switch phase {
			case phaseProbeShort:
				if awaiting {
					if gotResponse() {
						awaiting = false
						shortOk++
						DEBUG.Println(CLI, "adaptive heartbeat short ok", shortOk)
						if shortOk >= probeCount {
							phase = phaseProbeDoubling
							intervalMillis = shortMillis
							lastSuccessMillis = shortMillis
							sentAt = time.Time{}
							awaiting = false
						}
					} else if now.Sub(sentAt) >= responseWindow {
						WARN.Println(CLI, "adaptive heartbeat short probe failed -> connection lost")
						c.internalConnLost(errReceiveTimeout)
						return
					}
				} else if now.Sub(c.lastSent.Load().(time.Time)) >= time.Duration(intervalMillis)*time.Millisecond {
					sendProbe()
				}
			case phaseProbeDoubling, phaseProbeFine:
				if awaiting {
					if gotResponse() {
						awaiting = false
						lastSuccessMillis = intervalMillis
						DEBUG.Println(CLI, "adaptive heartbeat probe ok", intervalMillis/1000, "s")
						next := intervalMillis
						if phase == phaseProbeDoubling {
							next = intervalMillis * 2
							if next > ceilingMillis {
								next = ceilingMillis
							}
						} else {
							next = intervalMillis + fineStepMillis
						}
						if next <= intervalMillis {
							enterStable()
						} else {
							intervalMillis = next
						}
					} else if now.Sub(sentAt) >= responseWindow {
						INFO.Printf(CLI+"adaptive heartbeat probe failed at %ds, settling at %ds",
							intervalMillis/1000, lastSuccessMillis/1000)
						enterStable()
					}
				} else if now.Sub(c.lastSent.Load().(time.Time)) >= time.Duration(intervalMillis)*time.Millisecond {
					sendProbe()
				}
			case phaseStable:
				if now.Sub(c.lastReceived.Load().(time.Time)) >= time.Duration(successHeartMillis)*time.Millisecond*3/2 {
					WARN.Println(CLI, "adaptive heartbeat lost in stable state -> re-adapting")
					phase = phaseProbeShort
					shortOk = 0
					intervalMillis = shortMillis
					lastSuccessMillis = 0
					successHeartMillis = 0
					sentAt = time.Time{}
					awaiting = false
				} else if now.Sub(c.lastSent.Load().(time.Time)) >= time.Duration(successHeartMillis)*time.Millisecond {
					sendProbe()
				}
			}
		}
	}
}

// fixedKeepalive sends PINGREQ on a fixed cadence derived from effectiveHeartbeat (used when
// adaptive heartbeat has no headroom above the short period).
func fixedKeepalive(c *client, conn io.Writer) {
	heartbeat := c.effectiveHeartbeat()
	if heartbeat <= 0 {
		DEBUG.Println(CLI, "keepalive disabled (server_kp==0)")
		return
	}
	checkInterval := time.Duration(heartbeat) * time.Second / 4
	if checkInterval < time.Second {
		checkInterval = time.Second
	}
	intervalTicker := time.NewTicker(checkInterval)
	defer intervalTicker.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-intervalTicker.C:
			heartbeat = c.effectiveHeartbeat()
			if heartbeat <= 0 {
				continue
			}
			if time.Since(c.lastSent.Load().(time.Time)) >= time.Duration(heartbeat*int64(time.Second)) {
				ping := codec.NewControlPacket(codec.Pingreq).(*codec.PingreqPacket)
				if err := writePingReq(c, conn, ping); err != nil {
					ERROR.Println(err)
				}
				c.lastSent.Store(time.Now())
			}
			if time.Since(c.lastReceived.Load().(time.Time)) >= time.Duration(float64(heartbeat)*float64(time.Second)*1.5) {
				WARN.Println(CLI, "receive time out")
				c.internalConnLost(errReceiveTimeout)
				return
			}
		}
	}
}
