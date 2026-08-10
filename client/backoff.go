package client

import (
	"math/rand"
	"sync"
	"time"
)

type backoffController struct {
	sync.RWMutex
	statusMap map[string]*backoffStatus
}

type backoffStatus struct {
	lastSleepPeriod time.Duration
	lastErrorTime   time.Time
}

func newBackoffController() *backoffController {
	return &backoffController{
		statusMap: map[string]*backoffStatus{},
	}
}

func jitterDuration(d time.Duration, jitter float64) time.Duration {
	if jitter <= 0 {
		return d
	}
	factor := 1 - jitter + rand.Float64()*(2*jitter)
	return time.Duration(float64(d) * factor)
}

func (b *backoffController) getBackoffSleepTime(
	situation string, initSleepPeriod time.Duration, maxSleepPeriod time.Duration, processTime time.Duration, skipFirst bool,
) (time.Duration, bool) {
	var firstProcess = func(status *backoffStatus, init time.Duration, skip bool) (time.Duration, bool) {
		if skip {
			status.lastSleepPeriod = 0
			return 0, false
		}
		status.lastSleepPeriod = init
		return init, false
	}

	if initSleepPeriod > maxSleepPeriod {
		initSleepPeriod = maxSleepPeriod
	}
	b.Lock()
	defer b.Unlock()

	status, exist := b.statusMap[situation]
	if !exist {
		b.statusMap[situation] = &backoffStatus{initSleepPeriod, time.Now()}
		return firstProcess(b.statusMap[situation], initSleepPeriod, skipFirst)
	}

	oldTime := status.lastErrorTime
	status.lastErrorTime = time.Now()

	if status.lastErrorTime.Sub(oldTime) > (processTime*2 + status.lastSleepPeriod) {
		return firstProcess(status, initSleepPeriod, skipFirst)
	}

	if status.lastSleepPeriod == 0 {
		status.lastSleepPeriod = initSleepPeriod
		return initSleepPeriod, true
	}

	if nextSleepPeriod := status.lastSleepPeriod * 2; nextSleepPeriod <= maxSleepPeriod {
		status.lastSleepPeriod = nextSleepPeriod
	} else {
		status.lastSleepPeriod = maxSleepPeriod
	}

	return status.lastSleepPeriod, true
}

func (b *backoffController) sleepWithBackoff(
	situation string, initSleepPeriod time.Duration, maxSleepPeriod time.Duration, processTime time.Duration, skipFirst bool,
) (time.Duration, bool) {
	sleep, isFirst := b.getBackoffSleepTime(situation, initSleepPeriod, maxSleepPeriod, processTime, skipFirst)
	if sleep != 0 {
		time.Sleep(jitterDuration(sleep, 0.25))
	}
	return sleep, isFirst
}
