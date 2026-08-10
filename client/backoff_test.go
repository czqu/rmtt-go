package client

import (
	"testing"
	"time"
)

func TestJitterDuration_NoJitter(t *testing.T) {
	d := 3 * time.Second
	if got := jitterDuration(d, 0); got != d {
		t.Errorf("jitterDuration(%v, 0) = %v, want %v", d, got, d)
	}
	if got := jitterDuration(d, -1); got != d {
		t.Errorf("jitterDuration(%v, -1) = %v, want %v", d, got, d)
	}
}

func TestJitterDuration_Bounds(t *testing.T) {
	d := 1000 * time.Millisecond
	for i := 0; i < 100; i++ {
		got := jitterDuration(d, 0.25)
		low := time.Duration(float64(d) * 0.75)
		high := time.Duration(float64(d) * 1.25)
		if got < low || got >= high {
			t.Fatalf("jitterDuration(%v, 0.25) = %v, out of range [%v, %v)", d, got, low, high)
		}
	}
}

func TestGetBackoffSleepTime_FirstCall(t *testing.T) {
	b := newBackoffController()
	sleep, isFirst := b.getBackoffSleepTime("x", time.Second, 10*time.Second, time.Second, false)
	if sleep != time.Second {
		t.Errorf("first sleep = %v, want 1s", sleep)
	}
	if isFirst {
		t.Error("isFirst = true, want false")
	}
}

func TestGetBackoffSleepTime_FirstCallSkip(t *testing.T) {
	b := newBackoffController()
	sleep, isFirst := b.getBackoffSleepTime("x", time.Second, 10*time.Second, time.Second, true)
	if sleep != 0 {
		t.Errorf("sleep = %v, want 0", sleep)
	}
	if isFirst {
		t.Error("isFirst = true, want false")
	}

	sleep, isFirst = b.getBackoffSleepTime("x", time.Second, 10*time.Second, time.Second, false)
	if sleep != time.Second {
		t.Errorf("second sleep = %v, want 1s", sleep)
	}
	if !isFirst {
		t.Error("isFirst = false, want true")
	}
}

func TestGetBackoffSleepTime_DoublingAndCap(t *testing.T) {
	b := newBackoffController()
	// No reset between calls: processTime=0 and consecutive calls happen far
	// faster than the accumulated backoff period.
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 10 * time.Second, 10 * time.Second}
	for i, w := range want {
		sleep, isFirst := b.getBackoffSleepTime("x", time.Second, 10*time.Second, 0, false)
		if sleep != w {
			t.Errorf("call %d sleep = %v, want %v", i, sleep, w)
		}
		if i == 0 && isFirst {
			t.Errorf("call %d isFirst = true, want false", i)
		}
		if i > 0 && !isFirst {
			t.Errorf("call %d isFirst = false, want true", i)
		}
	}
}

func TestGetBackoffSleepTime_InitAboveMax(t *testing.T) {
	b := newBackoffController()
	sleep, _ := b.getBackoffSleepTime("x", 20*time.Second, 5*time.Second, 0, false)
	if sleep != 5*time.Second {
		t.Errorf("sleep = %v, want 5s (capped to max)", sleep)
	}
}

func TestGetBackoffSleepTime_ResetAfterIdle(t *testing.T) {
	b := &backoffController{
		statusMap: map[string]*backoffStatus{
			"idle": {lastSleepPeriod: 4 * time.Second, lastErrorTime: time.Now().Add(-time.Hour)},
		},
	}
	sleep, isFirst := b.getBackoffSleepTime("idle", time.Second, 10*time.Second, time.Second, false)
	if sleep != time.Second {
		t.Errorf("sleep = %v, want 1s after idle reset", sleep)
	}
	if isFirst {
		t.Error("isFirst = true, want false")
	}
}

func TestSleepWithBackoff_SkipFirstDoesNotSleep(t *testing.T) {
	b := newBackoffController()
	start := time.Now()
	sleep, _ := b.sleepWithBackoff("x", time.Second, 10*time.Second, time.Second, true)
	if sleep != 0 {
		t.Errorf("sleep = %v, want 0", sleep)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Error("sleepWithBackoff skipped first sleep but still blocked")
	}
}
