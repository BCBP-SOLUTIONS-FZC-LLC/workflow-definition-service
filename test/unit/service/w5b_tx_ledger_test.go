package service_test

import (
	"context"
	"sync"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"
)

type w5bTxKey struct{}

type w5bTx struct {
	mu         sync.Mutex
	committed  []string
	pending    []string
	started    int
	commits    int
	rollbacks  int
	retryStart int
	lastErr    error
}

var _ port.Transactor = (*w5bTx)(nil)

func (x *w5bTx) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	return x.run(ctx, fn, false)
}

func (x *w5bTx) RunInTxWithRetry(ctx context.Context, fn func(context.Context) error) error {
	return x.run(ctx, fn, true)
}

func (x *w5bTx) run(ctx context.Context, fn func(context.Context) error, retry bool) error {
	x.mu.Lock()
	x.started++
	if retry {
		x.retryStart++
	}
	x.pending = nil
	x.mu.Unlock()

	err := fn(context.WithValue(ctx, w5bTxKey{}, x))

	x.mu.Lock()
	defer x.mu.Unlock()
	if err != nil {
		x.rollbacks++
		x.pending = nil
		x.lastErr = err
		return err
	}
	x.commits++
	x.committed = append(x.committed, x.pending...)
	x.pending = nil
	return nil
}

func (x *w5bTx) write(ctx context.Context, what string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if ctx.Value(w5bTxKey{}) == x {
		x.pending = append(x.pending, what)
		return
	}
	x.committed = append(x.committed, what)
}

func (x *w5bTx) persisted() []string {
	x.mu.Lock()
	defer x.mu.Unlock()
	return append([]string(nil), x.committed...)
}

type w5bLog struct {
	testkit.FakeLogger
	mu    sync.Mutex
	infos []string
	warns []string
}

func (l *w5bLog) Info(msg string, _ map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infos = append(l.infos, msg)
}

func (l *w5bLog) Warn(msg string, _ map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, msg)
}
