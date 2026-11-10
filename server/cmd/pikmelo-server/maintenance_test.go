package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestStartupWaitsForVerifiedUpdateAndStopsOnShutdown(t *testing.T) {
	var blocked atomic.Bool
	blocked.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() { done <- waitForMaintenance(ctx, blocked.Load) }()
	select {
	case <-done:
		t.Fatal("startup work escaped maintenance")
	case <-time.After(20 * time.Millisecond):
	}
	blocked.Store(false)
	select {
	case ok := <-done:
		if !ok {
			t.Fatal("startup did not resume")
		}
	case <-time.After(time.Second):
		t.Fatal("startup work was never resumed")
	}
	cancel()
	blocked.Store(true)
	ctx, cancel = context.WithCancel(context.Background())
	go func() { done <- waitForMaintenance(ctx, blocked.Load) }()
	cancel()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("shutdown resumed background work")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel deferred work")
	}
}
