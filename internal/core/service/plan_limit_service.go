package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

var _ port.PlanLimits = (*PlanLimitService)(nil)

const planTableRetryBackoff = 5 * time.Second

type PlanLimitDeps struct {
	Reader port.PlanTableReader
	TTL    time.Duration
	Now    func() time.Time
	Log    port.Logger
}

type planTableRead struct {
	done  chan struct{}
	table map[string]*int64
	err   error
}

type PlanLimitService struct {
	reader port.PlanTableReader
	ttl    time.Duration
	now    func() time.Time
	log    port.Logger

	mu        sync.Mutex
	table     map[string]*int64
	fetchedAt time.Time
	retryAt   time.Time
	lastErr   error
	inflight  *planTableRead
}

func NewPlanLimitService(d PlanLimitDeps) *PlanLimitService {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &PlanLimitService{reader: d.Reader, ttl: d.TTL, now: now, log: logOrNoop(d.Log)}
}

func (s *PlanLimitService) WorkflowTemplateLimit(ctx context.Context, planCode string) (*int64, error) {
	code := strings.ToLower(strings.TrimSpace(planCode))
	if code == "" {
		return nil, fmt.Errorf("%w: no plan code on the request", domain.ErrUpstreamUnavailable)
	}
	table, err := s.currentTable(ctx, false)
	if err != nil {
		return nil, err
	}
	limit, ok := table[code]
	if !ok && s.tableAge() >= planTableRetryBackoff {
		if table, err = s.currentTable(ctx, true); err != nil {
			return nil, err
		}
		limit, ok = table[code]
	}
	if !ok {
		return nil, fmt.Errorf("%w: plan %q is not in the plan table", domain.ErrUpstreamUnavailable, code)
	}
	return limit, nil
}

func (s *PlanLimitService) tableAge() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.now().Sub(s.fetchedAt)
}

func (s *PlanLimitService) currentTable(ctx context.Context, force bool) (map[string]*int64, error) {
	s.mu.Lock()
	now := s.now()
	if s.table != nil && !force && now.Sub(s.fetchedAt) < s.ttl {
		defer s.mu.Unlock()
		return s.table, nil
	}
	if now.Before(s.retryAt) {
		defer s.mu.Unlock()
		return s.heldTableOrLastError()
	}
	if read := s.inflight; read != nil {
		held := s.table
		s.mu.Unlock()
		if held != nil {
			return held, nil
		}
		return s.awaitRead(ctx, read)
	}
	read := &planTableRead{done: make(chan struct{})}
	s.inflight = read
	s.mu.Unlock()

	read.table, read.err = s.reader.ReadPlanTable(context.WithoutCancel(ctx))

	s.mu.Lock()
	defer s.mu.Unlock()
	s.inflight = nil
	if read.err != nil {
		s.lastErr = read.err
		s.retryAt = s.now().Add(planTableRetryBackoff)
		if s.table != nil {
			s.log.Warn("plan table refresh failed, keeping the last table", map[string]any{"error": read.err.Error()})
		}
	} else {
		s.table = read.table
		s.fetchedAt = s.now()
		s.retryAt = time.Time{}
		s.lastErr = nil
	}
	close(read.done)
	return s.heldTableOrLastError()
}

func (s *PlanLimitService) awaitRead(ctx context.Context, read *planTableRead) (map[string]*int64, error) {
	select {
	case <-read.done:
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: waiting for the plan table: %w", domain.ErrUpstreamUnavailable, ctx.Err())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.heldTableOrLastError()
}

func (s *PlanLimitService) heldTableOrLastError() (map[string]*int64, error) {
	if s.table != nil {
		return s.table, nil
	}
	return nil, fmt.Errorf("%w: no plan table has been read: %w", domain.ErrUpstreamUnavailable, s.lastErr)
}
