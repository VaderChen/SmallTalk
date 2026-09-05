package main

import (
	"sync/atomic"
	"testing"
)

func TestBeforeServiceStopIsIdempotent(t *testing.T) {
	var stopped atomic.Int32
	service := &SmallTalkService{
		processStop: make(chan struct{}),
		stopWorkers: []func(){func() { stopped.Add(1) }},
	}

	service.BeforeServiceStop()
	service.BeforeServiceStop()

	if got := stopped.Load(); got != 1 {
		t.Fatalf("stop worker ran %d times, want 1", got)
	}
	select {
	case <-service.processStop:
	default:
		t.Fatal("processStop was not closed")
	}
}
