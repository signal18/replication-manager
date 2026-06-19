package cron

import (
	"testing"
	"time"
)

func TestStopBeforeStart(t *testing.T) {
	c := New()
	done := make(chan struct{})
	go func() {
		c.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() deadlocked on a never-started cron")
	}
}

func TestDoubleStop(t *testing.T) {
	c := New()
	c.Start()
	done := make(chan struct{})
	go func() {
		c.Stop()
		c.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("double Stop() deadlocked")
	}
}
