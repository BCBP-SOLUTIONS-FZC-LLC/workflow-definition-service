package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingLogger struct {
	nopLogger
	mu     sync.Mutex
	errors []string
	warns  []string
}

func (l *recordingLogger) Error(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, msg+" "+fieldText(fields))
}

func (l *recordingLogger) Warn(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, msg+" "+fieldText(fields))
}

func fieldText(fields map[string]any) string {
	var b strings.Builder
	for k, v := range fields {
		b.WriteString(k + "=")
		if s, ok := v.(string); ok {
			b.WriteString(s)
		}
		b.WriteString(" ")
	}
	return b.String()
}

func TestGoSafe_ARecoveredPanicIsLoggedAndDoesNotCrashTheProcess(t *testing.T) {
	log := &recordingLogger{}
	done := make(chan struct{})

	goSafe("exporter", log, func() {
		defer close(done)
		panic("boom")
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the goroutine never ran")
	}
	time.Sleep(50 * time.Millisecond)
	log.mu.Lock()
	defer log.mu.Unlock()
	if len(log.errors) != 1 || !strings.Contains(log.errors[0], "exporter") || !strings.Contains(log.errors[0], "boom") {
		t.Errorf("logged %v, want one error naming the goroutine and the panic", log.errors)
	}
}

func TestServe_AListenerFailureIsReportedNotAnOSExit(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	srv := &http.Server{Addr: taken.Addr().String(), Handler: http.NotFoundHandler()}
	fatal := make(chan error, 1)

	serve("http", srv, &recordingLogger{}, fatal)

	select {
	case err := <-fatal:
		if err == nil || !strings.Contains(err.Error(), "http") {
			t.Errorf("fatal = %v, want an error naming the listener", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a listener that cannot bind must be reported so the ordered shutdown runs")
	}
}

func TestServe_ACleanShutdownIsNotAFailure(t *testing.T) {
	srv := &http.Server{Addr: "127.0.0.1:0", Handler: http.NotFoundHandler()}
	fatal := make(chan error, 1)
	serve("http", srv, &recordingLogger{}, fatal)
	time.Sleep(100 * time.Millisecond)
	_ = srv.Shutdown(context.Background())

	select {
	case err := <-fatal:
		t.Errorf("a clean shutdown reported %v", err)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestWaitForStop_ReturnsOnASignalOrAListenerFailure(t *testing.T) {
	sig := make(chan os.Signal, 1)
	fatal := make(chan error, 1)

	sig <- os.Interrupt
	if reason, failed := waitForStop(sig, fatal); failed || !strings.Contains(reason, "interrupt") {
		t.Errorf("signal: reason %q failed %v", reason, failed)
	}

	fatal <- errors.New("http: address in use")
	if reason, failed := waitForStop(sig, fatal); !failed || !strings.Contains(reason, "address in use") {
		t.Errorf("listener failure: reason %q failed %v", reason, failed)
	}
}

func TestDrainWithTimeout_EachDrainGetsItsOwnBudget(t *testing.T) {
	log := &recordingLogger{}
	stuck := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}

	begin := time.Now()
	drainWithTimeout(log, "db pool", 100*time.Millisecond, stuck)
	drainWithTimeout(log, "system db pool", 100*time.Millisecond, stuck)
	elapsed := time.Since(begin)

	if elapsed < 180*time.Millisecond {
		t.Errorf("both drains took %s: the second must start with a fresh budget, not what the first left over", elapsed)
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	if len(log.warns) != 2 {
		t.Errorf("warnings = %v, want one per timed-out drain", log.warns)
	}
}

func TestGoSafeRestart_ARecoveredPanicRestartsTheWorkUntilItEnds(t *testing.T) {
	log := &recordingLogger{}
	var mu sync.Mutex
	runs := 0
	finished := make(chan struct{})

	goSafeRestart(context.Background(), "rls-exporter", log, time.Millisecond, func() {
		mu.Lock()
		runs++
		n := runs
		mu.Unlock()
		if n < 3 {
			panic("boom")
		}
		close(finished)
	})

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("the work was not restarted after a panic: the exporter would stay dead while the pod reports healthy")
	}
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if runs != 3 {
		t.Errorf("runs = %d, want 3 (two panics, then a normal return that ends the loop)", runs)
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	if len(log.errors) != 2 {
		t.Errorf("logged %d panics, want 2", len(log.errors))
	}
}

func TestGoSafeRestart_StopsRestartingWhenTheContextEnds(t *testing.T) {
	log := &recordingLogger{}
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	runs := 0

	goSafeRestart(ctx, "rls-exporter", log, 5*time.Millisecond, func() {
		mu.Lock()
		runs++
		mu.Unlock()
		cancel()
		panic("boom")
	})

	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if runs != 1 {
		t.Errorf("runs = %d after the context ended, want 1", runs)
	}
}
