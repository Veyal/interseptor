package main

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type shutdownFunc func(context.Context)

func (f shutdownFunc) Shutdown(ctx context.Context) {
	f(ctx)
}

type closeFunc func()

func (f closeFunc) Close() {
	f()
}

func TestShutdownRuntimeSlowControlDoesNotStarveProxy(t *testing.T) {
	// Given
	controlStarted := make(chan struct{})
	proxyStarted := make(chan struct{})
	releaseControl := make(chan struct{})
	var active atomic.Int32
	var hubClosed atomic.Bool

	control := shutdownFunc(func(ctx context.Context) {
		active.Add(1)
		defer active.Add(-1)
		close(controlStarted)
		<-releaseControl
	})
	proxy := shutdownFunc(func(ctx context.Context) {
		active.Add(1)
		defer active.Add(-1)
		if err := ctx.Err(); err != nil {
			t.Errorf("proxy shutdown context expired before drain: %v", err)
		}
		close(proxyStarted)
	})
	hub := closeFunc(func() {
		if active.Load() != 0 {
			t.Error("hub closed while shutdown handlers were active")
		}
		hubClosed.Store(true)
	})

	// When
	done := make(chan struct{})
	go func() {
		shutdownRuntime(time.Second, control, hub, proxy)
		close(done)
	}()
	<-controlStarted
	<-proxyStarted
	close(releaseControl)
	<-done

	// Then
	if !hubClosed.Load() {
		t.Error("hub was not closed after server drains")
	}
}

type streamHub struct {
	closeFunc
	streams func()
}

func (h streamHub) CloseStreams() { h.streams() }

// The hub's long-lived streams end before the servers drain, so an open UI
// event stream cannot hold the process for the whole shutdown timeout.
func TestShutdownRuntimeEndsStreamsBeforeDrain(t *testing.T) {
	var streamsClosed atomic.Bool
	control := shutdownFunc(func(ctx context.Context) {
		if !streamsClosed.Load() {
			t.Error("control server drained before the hub's streams were closed")
		}
	})
	proxy := shutdownFunc(func(ctx context.Context) {})
	hub := streamHub{closeFunc: func() {}, streams: func() { streamsClosed.Store(true) }}
	shutdownRuntime(time.Second, control, hub, proxy)
	if !streamsClosed.Load() {
		t.Fatal("CloseStreams was not called")
	}
}
