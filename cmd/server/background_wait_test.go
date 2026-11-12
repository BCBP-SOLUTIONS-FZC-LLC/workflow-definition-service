package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApp_WaitForBackground_ReturnsOnlyOnceEveryWorkerHasEnded(t *testing.T) {
	exporter, purger := make(chan struct{}), make(chan struct{})
	a := &app{log: &recordingLogger{}, background: []<-chan struct{}{exporter, purger}}
	returned := make(chan struct{})

	go func() { a.waitForBackground(context.Background()); close(returned) }()

	close(exporter)
	select {
	case <-returned:
		t.Fatal("returned while the purger was still running")
	case <-time.After(20 * time.Millisecond):
	}
	close(purger)
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("did not return after both workers ended")
	}
}

func TestApp_WaitForBackground_GivesUpWithAWarningWhenTheGraceEnds(t *testing.T) {
	log := &recordingLogger{}
	a := &app{log: log, background: []<-chan struct{}{make(chan struct{})}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	a.waitForBackground(ctx)

	log.mu.Lock()
	defer log.mu.Unlock()
	require.Len(t, log.warns, 1)
	assert.Contains(t, log.warns[0], "did not stop")
}

func TestApp_WaitForBackground_WithNoWorkersReturnsAtOnce(t *testing.T) {
	(&app{log: &recordingLogger{}}).waitForBackground(context.Background())
}
