package service_test

import (
	"sync"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"
)

// coreSvcALog records the messages written at info and warn level.
type coreSvcALog struct {
	testkit.FakeLogger
	mu    sync.Mutex
	infos []string
	warns []string
}

func (l *coreSvcALog) Info(msg string, _ map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infos = append(l.infos, msg)
}

func (l *coreSvcALog) Warn(msg string, _ map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, msg)
}
