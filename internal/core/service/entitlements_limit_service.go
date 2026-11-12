package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"runtime/debug"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

var _ port.PlanLimits = (*EntitlementsLimitService)(nil)

const (
	entitlementsRetryBackoff       = 5 * time.Second
	entitlementsCacheCap           = 10_000
	entitlementsMaxConcurrentReads = 32
	// entitlementsJitter spreads the TTL and the backoff by up to this fraction either way, so tenants read at the same moment do not expire together.
	entitlementsJitter = 0.2
)

type EntitlementsLimitDeps struct {
	Reader         port.EntitlementsReader
	TTL            time.Duration
	FreshnessBound time.Duration
	Now            func() time.Time
	Log            port.Logger
	// Rand returns a number in [0, 1) for the jitter; nil uses math/rand. It is only called under the cache's lock.
	Rand func() float64
}

type entitlementsEntry struct {
	has       bool
	limit     *int64
	fetchedAt time.Time
	ttl       time.Duration
	retryAt   time.Time
	lastErr   error
}

type entitlementsRead struct {
	done  chan struct{}
	limit *int64
	err   error
	// held says the entry still holds a limit within the freshness bound when the read failed; a caller that waited
	// for the read falls back to heldLimit (nil is the unlimited value). heldAge is that limit's age at the failure.
	held      bool
	heldLimit *int64
	heldAge   time.Duration
}

// EntitlementsLimitService caches each tenant's workflow template limit (definition LLD §5.6.4). A value older than its
// jittered TTL but younger than twice that TTL (and within the freshness bound) is served as it is while one background
// read renews it. An older value makes the caller wait for the read; only if that read fails is the value still within
// the freshness bound served, and then it counts as a stale serve.
type EntitlementsLimitService struct {
	reader port.EntitlementsReader
	ttl    time.Duration
	bound  time.Duration
	now    func() time.Time
	rand   func() float64
	log    port.Logger
	slots  chan struct{}

	mu       sync.Mutex
	entries  map[uuid.UUID]*entitlementsEntry
	inflight map[uuid.UUID]*entitlementsRead
	// servedPeak is the oldest age served stale in the current window of one TTL; the age gauge shows it.
	servedPeak  time.Duration
	servedUntil time.Time
}

func NewEntitlementsLimitService(d EntitlementsLimitDeps) (*EntitlementsLimitService, error) {
	switch {
	case d.Reader == nil:
		return nil, errors.New("entitlements limit: a reader is required")
	case d.TTL <= 0:
		return nil, fmt.Errorf("entitlements limit: the TTL must be positive, got %s", d.TTL)
	case d.FreshnessBound < d.TTL:
		return nil, fmt.Errorf("entitlements limit: the freshness bound %s is below the TTL %s", d.FreshnessBound, d.TTL)
	}
	now := d.Now
	if now == nil {
		now = time.Now
	}
	random := d.Rand
	if random == nil {
		random = rand.Float64
	}
	return &EntitlementsLimitService{
		reader:   d.Reader,
		ttl:      d.TTL,
		bound:    d.FreshnessBound,
		now:      now,
		rand:     random,
		log:      logOrNoop(d.Log),
		slots:    make(chan struct{}, entitlementsMaxConcurrentReads),
		entries:  map[uuid.UUID]*entitlementsEntry{},
		inflight: map[uuid.UUID]*entitlementsRead{},
	}, nil
}

// WorkflowTemplateLimit decides under the lock and acts after it: nothing is logged and no error is built while the lock is held.
func (s *EntitlementsLimitService) WorkflowTemplateLimit(ctx context.Context, tenantID uuid.UUID, _ string) (*int64, error) {
	s.mu.Lock()
	now := s.now()
	e := s.entries[tenantID]
	switch {
	case e != nil && e.has && now.Sub(e.fetchedAt) < e.ttl:
		limit := e.limit
		gauge, changed := s.noteServedLocked(now, 0)
		s.mu.Unlock()
		s.publishAge(gauge, changed)
		return limit, nil
	case e != nil && e.has && now.Sub(e.fetchedAt) < s.staleWindow(e):
		s.refreshInBackgroundLocked(ctx, tenantID, e, now)
		return s.serveStale(e.limit, now.Sub(e.fetchedAt), now), nil
	case e != nil && now.Before(e.retryAt):
		// A read failed a moment ago: no new read, so the value held within the bound is all there is to serve.
		if e.has && now.Sub(e.fetchedAt) < s.bound {
			return s.serveStale(e.limit, now.Sub(e.fetchedAt), now), nil
		}
		cause := e.lastErr
		s.mu.Unlock()
		return nil, s.unavailable(tenantID, cause)
	}
	read := s.startReadLocked(ctx, tenantID, false)
	s.mu.Unlock()
	return s.await(ctx, tenantID, read)
}

// staleWindow is the age below which a value past its TTL is served while a background read renews it: twice the
// entry's TTL, never beyond the freshness bound.
func (s *EntitlementsLimitService) staleWindow(e *entitlementsEntry) time.Duration {
	return min(2*e.ttl, s.bound)
}

// serveStale is called with the lock held and releases it; it counts the serve and publishes the age gauge.
func (s *EntitlementsLimitService) serveStale(limit *int64, age time.Duration, now time.Time) *int64 {
	gauge, changed := s.noteServedLocked(now, age)
	s.mu.Unlock()
	observability.IncCounter(observability.EntitlementsStaleServedTotal)
	s.publishAge(gauge, changed)
	return limit
}

// refreshInBackgroundLocked starts at most one read for the tenant; with no free slot it does nothing and a later call retries.
func (s *EntitlementsLimitService) refreshInBackgroundLocked(ctx context.Context, tenantID uuid.UUID, e *entitlementsEntry, now time.Time) {
	if s.inflight[tenantID] != nil || now.Before(e.retryAt) {
		return
	}
	select {
	case s.slots <- struct{}{}:
		s.startReadLocked(ctx, tenantID, true)
	default:
	}
}

// startReadLocked joins the tenant's running read or starts one. The read outlives the caller that started it,
// so one caller giving up does not fail the others; a blocking read queues for its slot inside the read.
func (s *EntitlementsLimitService) startReadLocked(ctx context.Context, tenantID uuid.UUID, slotHeld bool) *entitlementsRead {
	if read := s.inflight[tenantID]; read != nil {
		return read
	}
	read := &entitlementsRead{done: make(chan struct{})}
	s.inflight[tenantID] = read
	go s.runRead(context.WithoutCancel(ctx), tenantID, read, slotHeld)
	return read
}

func (s *EntitlementsLimitService) runRead(ctx context.Context, tenantID uuid.UUID, read *entitlementsRead, slotHeld bool) {
	if !slotHeld {
		s.slots <- struct{}{}
	}
	defer func() { <-s.slots }()
	limit, err := s.callReader(ctx, tenantID)
	s.settleRead(tenantID, read, limit, err)
}

// callReader turns a reader panic into a failed read, logged at Error with its stack.
func (s *EntitlementsLimitService) callReader(ctx context.Context, tenantID uuid.UUID) (limit *int64, err error) {
	defer func() {
		if r := recover(); r != nil {
			limit = nil
			err = fmt.Errorf("%w: reading entitlements panicked: %v", domain.ErrUpstreamUnavailable, r)
			observability.IncCounter(observability.EntitlementsReadPanicsTotal)
			s.log.Error("entitlements reader panicked", map[string]any{
				"tenant_id": tenantID.String(),
				"panic":     fmt.Sprint(r),
				"stack":     string(debug.Stack()),
			})
		}
	}()
	return s.reader.ReadWorkflowTemplateLimit(ctx, tenantID)
}

func (s *EntitlementsLimitService) settleRead(tenantID uuid.UUID, read *entitlementsRead, limit *int64, err error) {
	s.mu.Lock()
	delete(s.inflight, tenantID)
	now := s.now()
	e := s.entries[tenantID]
	if e == nil {
		e = s.newEntryLocked(tenantID, now)
	}
	var heldAge time.Duration
	held := false
	if err == nil {
		read.limit = limit
		e.has, e.limit, e.fetchedAt = true, limit, now
		e.ttl = min(s.jittered(s.ttl), s.bound)
		e.retryAt, e.lastErr = time.Time{}, nil
	} else {
		read.err = err
		e.retryAt, e.lastErr = now.Add(s.jittered(entitlementsRetryBackoff)), err
		heldAge = now.Sub(e.fetchedAt)
		held = e.has && heldAge < s.bound
		if held {
			read.held, read.heldLimit, read.heldAge = true, e.limit, heldAge
		}
	}
	close(read.done)
	s.mu.Unlock()

	if err == nil {
		return
	}
	fields := map[string]any{"tenant_id": tenantID.String(), "error": err.Error()}
	if held {
		fields["held_age_seconds"] = heldAge.Seconds()
		s.log.Warn("entitlements refresh failed, serving the held limit", fields)
		return
	}
	s.log.Error("entitlements read failed and no limit is held within the freshness bound", fields)
}

func (s *EntitlementsLimitService) await(ctx context.Context, tenantID uuid.UUID, read *entitlementsRead) (*int64, error) {
	select {
	case <-read.done:
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: waiting for the entitlements read: %w", domain.ErrUpstreamUnavailable, ctx.Err())
	}
	if read.err != nil {
		if read.held {
			s.mu.Lock()
			return s.serveStale(read.heldLimit, read.heldAge, s.now()), nil
		}
		return nil, s.unavailable(tenantID, read.err)
	}
	return read.limit, nil
}

func (s *EntitlementsLimitService) unavailable(tenantID uuid.UUID, cause error) error {
	return fmt.Errorf("%w: the workflow template limit of tenant %s has not been read within %s: %w",
		domain.ErrUpstreamUnavailable, tenantID, s.bound, cause)
}

func (s *EntitlementsLimitService) jittered(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (1 - entitlementsJitter + 2*entitlementsJitter*s.rand()))
}

// noteServedLocked tracks the oldest age served stale in the current TTL window and says whether the gauge changes.
// Every call counts, so the gauge falls back to 0 once a window passes with no stale serve.
func (s *EntitlementsLimitService) noteServedLocked(now time.Time, staleAge time.Duration) (gauge float64, changed bool) {
	if !now.Before(s.servedUntil) {
		changed = s.servedPeak != 0
		s.servedPeak, s.servedUntil = 0, now.Add(s.ttl)
	}
	if staleAge > s.servedPeak {
		s.servedPeak, changed = staleAge, true
	}
	return s.servedPeak.Seconds(), changed
}

func (s *EntitlementsLimitService) publishAge(gauge float64, changed bool) {
	if changed {
		observability.SetGauge(observability.EntitlementsOldestServedAgeSeconds, gauge)
	}
}

func (s *EntitlementsLimitService) newEntryLocked(tenantID uuid.UUID, now time.Time) *entitlementsEntry {
	if len(s.entries) >= entitlementsCacheCap {
		s.makeRoomLocked(now)
	}
	e := &entitlementsEntry{}
	s.entries[tenantID] = e
	return e
}

func (s *EntitlementsLimitService) makeRoomLocked(now time.Time) {
	for id, e := range s.entries {
		if s.expiredLocked(e, now) {
			delete(s.entries, id)
		}
	}
	if len(s.entries) < entitlementsCacheCap {
		return
	}
	var oldestID uuid.UUID
	var oldest time.Time
	first := true
	for id, e := range s.entries {
		if first || e.fetchedAt.Before(oldest) {
			oldestID, oldest, first = id, e.fetchedAt, false
		}
	}
	delete(s.entries, oldestID)
}

func (s *EntitlementsLimitService) expiredLocked(e *entitlementsEntry, now time.Time) bool {
	if e.has {
		return now.Sub(e.fetchedAt) >= s.bound
	}
	return !now.Before(e.retryAt)
}
