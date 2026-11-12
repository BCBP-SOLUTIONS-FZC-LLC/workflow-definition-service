//go:build integration

package main

import (
	"os"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApp_RunUntil_AStopSignalFlipsReadinessThenWaitsTheDrainDelayThenStopsEverything(t *testing.T) {
	r := cmdSrvCovBuild(t)
	const drain = 7 * time.Second
	r.app.cfg.ShutdownDrainDelay = drain

	var mu sync.Mutex
	var slept []time.Duration
	var drainsAtSleep []int32
	sleep := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		slept = append(slept, d)
		drainsAtSleep = append(drainsAtSleep, r.drains.Load())
	}
	stop := make(chan os.Signal, 1)

	done := make(chan error, 1)
	go func() { done <- r.app.runUntil(stop, sleep) }()
	cmdSrvCovAwait(t, "the HTTP server to listen", r.httpAddr)
	cmdSrvCovAwait(t, "the metrics server to listen", r.metricsAddr)
	stop <- syscall.SIGTERM

	select {
	case err := <-done:
		require.NoError(t, err, "a stop signal is a clean exit")
	case <-time.After(cmdSrvCovWait):
		t.Fatal("runUntil did not return after the stop signal")
	}

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []time.Duration{drain}, slept, "the drain delay is waited once, in full")
	assert.Equal(t, []int32{1}, drainsAtSleep, "readiness flips to draining before the wait, not after")
	shutting := cmdSrvCovLogIndex(r.rec, "info", "shutting down")
	stopped := cmdSrvCovLogIndex(r.rec, "info", "server stopped")
	require.GreaterOrEqual(t, shutting, 0)
	assert.Less(t, shutting, stopped)
	for _, e := range r.rec.snapshot() {
		if e.msg == "shutting down" {
			assert.Equal(t, "terminated", e.fields["reason"])
		}
	}
	cmdSrvCovAssertStopped(t, r)
}
