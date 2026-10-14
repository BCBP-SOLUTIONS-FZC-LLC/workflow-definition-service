package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime/debug"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

func goSafe(name string, log port.Logger, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Error("goroutine panicked", map[string]any{
					"goroutine": name,
					"panic":     fmt.Sprint(r),
					"stack":     string(debug.Stack()),
				})
			}
		}()
		fn()
	}()
}

func goSafeRestart(ctx context.Context, name string, log port.Logger, delay time.Duration, fn func()) {
	go func() {
		for {
			if runRecovered(name, log, fn) {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
	}()
}

func runRecovered(name string, log port.Logger, fn func()) (completed bool) {
	defer func() {
		if r := recover(); r != nil {
			log.Error("goroutine panicked, restarting", map[string]any{
				"goroutine": name,
				"panic":     fmt.Sprint(r),
				"stack":     string(debug.Stack()),
			})
		}
	}()
	fn()
	return true
}

func serve(name string, srv *http.Server, log port.Logger, fatal chan<- error) {
	goSafe(name, log, func() {
		log.Info(name+" server starting", map[string]any{"addr": srv.Addr})
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			select {
			case fatal <- fmt.Errorf("%s server: %w", name, err):
			default:
			}
		}
	})
}

func waitForStop(sig <-chan os.Signal, fatal <-chan error) (reason string, failed bool) {
	select {
	case s := <-sig:
		return s.String(), false
	case err := <-fatal:
		return err.Error(), true
	}
}

func drainWithTimeout(log port.Logger, name string, timeout time.Duration, drain func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := drain(ctx); err != nil {
		log.Warn(name+" drain timed out", map[string]any{"error": err.Error(), "timeout": timeout.String()})
	}
}
