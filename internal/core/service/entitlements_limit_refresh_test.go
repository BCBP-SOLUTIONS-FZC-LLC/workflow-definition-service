package service

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/logger"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

const (
	testTTL   = time.Minute
	testBound = 10 * time.Minute
)

func initTestMetrics(t *testing.T) {
	t.Helper()
	log, err := logger.NewLogger("dev")
	require.NoError(t, err)
	_ = gincommon.ObservabilityMiddlewares(gincommon.Config{
		Logger: log, ServiceName: "workflow-definition-service", BuildVersion: "test",
		Domain: observability.Domain, Environment: "test",
	})
	observability.Register()
}

// gatedReader is a reader the test steers: it can wait on a gate, fail, panic or answer with its context's error.
type gatedReader struct {
	mu        sync.Mutex
	calls     map[uuid.UUID]int
	limit     *int64
	err       error
	gate      chan struct{}
	panicWith any
	ctxErr    bool
	active    int
	peak      int
}

func newGatedReader(limit int64) *gatedReader {
	return &gatedReader{calls: map[uuid.UUID]int{}, limit: &limit}
}

func (r *gatedReader) ReadWorkflowTemplateLimit(ctx context.Context, tenantID uuid.UUID) (*int64, error) {
	r.mu.Lock()
	r.calls[tenantID]++
	r.active++
	r.peak = max(r.peak, r.active)
	gate, boom, ctxErr := r.gate, r.panicWith, r.ctxErr
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.active--
		r.mu.Unlock()
	}()
	if boom != nil {
		panic(boom)
	}
	if gate != nil {
		<-gate
	}
	if ctxErr && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	return r.limit, nil
}

func (r *gatedReader) callsFor(id uuid.UUID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[id]
}

func (r *gatedReader) activeNow() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

func (r *gatedReader) peakActive() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.peak
}

func (r *gatedReader) set(mutate func(*gatedReader)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	mutate(r)
}

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// testRand is the injected jitter source; 0.5 means "no jitter" (a factor of exactly 1).
type testRand struct{ bits atomic.Uint64 }

func newTestRand(v float64) *testRand {
	r := &testRand{}
	r.set(v)
	return r
}

func (r *testRand) set(v float64) { r.bits.Store(math.Float64bits(v)) }
func (r *testRand) next() float64 { return math.Float64frombits(r.bits.Load()) }

type logLine struct {
	level  string
	msg    string
	fields map[string]any
}

// recLog records every line and runs onLog first, which the lock test uses to probe the cache's mutex.
type recLog struct {
	mu    sync.Mutex
	lines []logLine
	onLog func()
}

func (l *recLog) record(level, msg string, fields map[string]any) {
	if l.onLog != nil {
		l.onLog()
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, logLine{level, msg, fields})
}

func (l *recLog) Debug(msg string, f map[string]any) { l.record("debug", msg, f) }
func (l *recLog) Info(msg string, f map[string]any)  { l.record("info", msg, f) }
func (l *recLog) Warn(msg string, f map[string]any)  { l.record("warn", msg, f) }
func (l *recLog) Error(msg string, f map[string]any) { l.record("error", msg, f) }

// waitFor returns the lines at level once there are n of them: a read logs after it unlocks, so it can trail settled.
func (l *recLog) waitFor(t *testing.T, level string, n int) []logLine {
	t.Helper()
	require.Eventually(t, func() bool { return len(l.at(level)) >= n }, 5*time.Second, time.Millisecond)
	return l.at(level)
}

func (l *recLog) at(level string) []logLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []logLine
	for _, line := range l.lines {
		if line.level == level {
			out = append(out, line)
		}
	}
	return out
}

type limitHarness struct {
	svc    *EntitlementsLimitService
	reader *gatedReader
	clock  *testClock
	rnd    *testRand
	log    *recLog
}

func newHarness(t *testing.T, reader *gatedReader) *limitHarness {
	t.Helper()
	initTestMetrics(t)
	h := &limitHarness{
		reader: reader,
		clock:  &testClock{now: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)},
		rnd:    newTestRand(0.5),
		log:    &recLog{},
	}
	svc, err := NewEntitlementsLimitService(EntitlementsLimitDeps{
		Reader: reader, TTL: testTTL, FreshnessBound: testBound, Now: h.clock.Now, Log: h.log, Rand: h.rnd.next,
	})
	require.NoError(t, err)
	h.svc = svc
	return h
}

func (h *limitHarness) limitOf(ctx context.Context, tenant uuid.UUID) (*int64, error) {
	return h.svc.WorkflowTemplateLimit(ctx, tenant, "")
}

func (h *limitHarness) inflightCount() int {
	h.svc.mu.Lock()
	defer h.svc.mu.Unlock()
	return len(h.svc.inflight)
}

func (h *limitHarness) isInflight(id uuid.UUID) bool {
	h.svc.mu.Lock()
	defer h.svc.mu.Unlock()
	return h.svc.inflight[id] != nil
}

func (h *limitHarness) entryCount() int {
	h.svc.mu.Lock()
	defer h.svc.mu.Unlock()
	return len(h.svc.entries)
}

func (h *limitHarness) settled(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool { return h.inflightCount() == 0 }, 5*time.Second, time.Millisecond)
}

// prime reads the tenant once so a held value exists.
func (h *limitHarness) prime(t *testing.T, tenant uuid.UUID) {
	t.Helper()
	got, err := h.limitOf(context.Background(), tenant)
	require.NoError(t, err)
	require.NotNil(t, got)
	h.settled(t)
}

func staleServed() float64 { return testutil.ToFloat64(observability.EntitlementsStaleServedTotal) }

func TestNewEntitlementsLimitService_RefusesWhatItCannotRunWith(t *testing.T) {
	reader := newGatedReader(1)
	tests := []struct {
		name string
		deps EntitlementsLimitDeps
		want string
	}{
		{"a nil reader", EntitlementsLimitDeps{TTL: testTTL, FreshnessBound: testBound}, "reader"},
		{"a zero TTL", EntitlementsLimitDeps{Reader: reader, FreshnessBound: testBound}, "TTL"},
		{"a negative TTL", EntitlementsLimitDeps{Reader: reader, TTL: -time.Second, FreshnessBound: testBound}, "TTL"},
		{"a bound below the TTL", EntitlementsLimitDeps{Reader: reader, TTL: testTTL, FreshnessBound: testTTL - 1}, "freshness bound"},
		{"no bound", EntitlementsLimitDeps{Reader: reader, TTL: testTTL}, "freshness bound"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, err := NewEntitlementsLimitService(tt.deps)

			require.Error(t, err)
			assert.ErrorContains(t, err, tt.want)
			assert.Nil(t, svc)
		})
	}
	svc, err := NewEntitlementsLimitService(EntitlementsLimitDeps{Reader: reader, TTL: testTTL, FreshnessBound: testTTL})
	require.NoError(t, err, "a bound equal to the TTL is allowed")
	assert.NotNil(t, svc)
}

func TestEntitlementsLimitService_TheTTLIsJitteredPerEntryBetweenEightyAndOneHundredTwentyPercent(t *testing.T) {
	tests := []struct {
		name      string
		random    float64
		justFresh time.Duration
		justStale time.Duration
		wantTTL   time.Duration
	}{
		{"the lowest draw", 0, 47 * time.Second, 48 * time.Second, 48 * time.Second},
		{"no jitter", 0.5, 59 * time.Second, 60 * time.Second, 60 * time.Second},
		{"the highest draw", 0.999999, 71 * time.Second, 73 * time.Second, 72 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, newGatedReader(1))
			h.rnd.set(tt.random)
			tenant := uuid.New()
			h.prime(t, tenant)
			h.svc.mu.Lock()
			ttl := h.svc.entries[tenant].ttl
			h.svc.mu.Unlock()
			assert.InDelta(t, tt.wantTTL.Seconds(), ttl.Seconds(), 0.001)

			before := staleServed()
			h.clock.advance(tt.justFresh)
			_, err := h.limitOf(context.Background(), tenant)
			require.NoError(t, err)
			assert.Equal(t, before, staleServed(), "younger than the entry's TTL: served as is")
			assert.False(t, h.isInflight(tenant))

			h.clock.advance(tt.justStale - tt.justFresh)
			_, err = h.limitOf(context.Background(), tenant)
			require.NoError(t, err)
			assert.Equal(t, before+1, staleServed(), "older than the entry's TTL: served stale")
		})
	}
}

func TestEntitlementsLimitService_TheJitteredTTLNeverOutlivesTheFreshnessBound(t *testing.T) {
	h := newHarness(t, newGatedReader(1))
	svc, err := NewEntitlementsLimitService(EntitlementsLimitDeps{
		Reader: h.reader, TTL: testTTL, FreshnessBound: testTTL, Now: h.clock.Now, Rand: newTestRand(0.999999).next,
	})
	require.NoError(t, err)
	tenant := uuid.New()
	_, err = svc.WorkflowTemplateLimit(context.Background(), tenant, "")
	require.NoError(t, err)

	svc.mu.Lock()
	ttl := svc.entries[tenant].ttl
	svc.mu.Unlock()

	assert.Equal(t, testTTL, ttl)
}

func TestEntitlementsLimitService_TheBackoffIsJitteredToo(t *testing.T) {
	tests := []struct {
		name         string
		random       float64
		stillBlocked time.Duration
		reads        time.Duration
	}{
		{"the lowest draw", 0, 3900 * time.Millisecond, 4 * time.Second},
		{"the highest draw", 0.999999, 5900 * time.Millisecond, 6 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := newGatedReader(1)
			reader.err = errors.New("down")
			h := newHarness(t, reader)
			h.rnd.set(tt.random)
			tenant := uuid.New()
			_, err := h.limitOf(context.Background(), tenant)
			require.Error(t, err)

			h.clock.advance(tt.stillBlocked)
			_, err = h.limitOf(context.Background(), tenant)
			require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
			assert.Equal(t, 1, reader.callsFor(tenant), "no read inside the backoff")

			h.clock.advance(tt.reads - tt.stillBlocked + time.Millisecond)
			_, err = h.limitOf(context.Background(), tenant)
			require.Error(t, err)
			assert.Equal(t, 2, reader.callsFor(tenant), "the backoff is over")
		})
	}
}

func TestEntitlementsLimitService_AStaleValueIsServedAtOnceWhileTheReadRunsAndTheNewValueAppearsAfterIt(t *testing.T) {
	reader := newGatedReader(80)
	h := newHarness(t, reader)
	tenant := uuid.New()
	h.prime(t, tenant)
	gate := make(chan struct{})
	reader.set(func(r *gatedReader) { r.gate, r.limit = gate, ptr(120) })
	h.clock.advance(testTTL)
	before := staleServed()

	answered := make(chan *int64, 1)
	go func() {
		got, err := h.limitOf(context.Background(), tenant)
		assert.NoError(t, err)
		answered <- got
	}()
	select {
	case got := <-answered:
		require.NotNil(t, got)
		assert.Equal(t, int64(80), *got, "the held value answers")
	case <-time.After(5 * time.Second):
		t.Fatal("the caller waited for the reader")
	}
	require.Eventually(t, func() bool { return reader.callsFor(tenant) == 2 }, 5*time.Second, time.Millisecond)
	assert.Equal(t, 1, reader.activeNow(), "the reader is still running")
	assert.Equal(t, before+1, staleServed())

	close(gate)
	h.settled(t)
	got, err := h.limitOf(context.Background(), tenant)
	require.NoError(t, err)
	assert.Equal(t, int64(120), *got, "the renewed value replaced the held one")
	assert.Equal(t, 2, reader.callsFor(tenant))
}

func TestEntitlementsLimitService_AtOneAndAHalfTTLTheHeldValueIsServedWithoutWaitingForTheReader(t *testing.T) {
	reader := newGatedReader(80)
	h := newHarness(t, reader)
	tenant := uuid.New()
	h.prime(t, tenant)
	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	reader.set(func(r *gatedReader) { r.gate, r.limit = gate, ptr(120) })
	h.clock.advance(testTTL * 3 / 2)
	before := staleServed()

	got, err := h.limitOf(context.Background(), tenant)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(80), *got, "the held value answered while the reader is still blocked")
	assert.Equal(t, before+1, staleServed())
	require.Eventually(t, func() bool { return reader.callsFor(tenant) == 2 }, 5*time.Second, time.Millisecond)
}

func TestEntitlementsLimitService_AtTwoAndAHalfTTLTheCallerWaitsForTheReaderAndGetsTheNewValue(t *testing.T) {
	reader := newGatedReader(80)
	h := newHarness(t, reader)
	tenant := uuid.New()
	h.prime(t, tenant)
	gate := make(chan struct{})
	reader.set(func(r *gatedReader) { r.gate, r.limit = gate, ptr(120) })
	h.clock.advance(testTTL * 5 / 2)
	before := staleServed()

	result := make(chan *int64, 1)
	go func() {
		got, err := h.limitOf(context.Background(), tenant)
		assert.NoError(t, err)
		result <- got
	}()
	require.Eventually(t, func() bool { return reader.callsFor(tenant) == 2 }, 5*time.Second, time.Millisecond)
	select {
	case <-result:
		t.Fatal("the caller must wait for the reader, not get the held value")
	case <-time.After(50 * time.Millisecond):
	}

	close(gate)
	got := <-result
	require.NotNil(t, got)
	assert.Equal(t, int64(120), *got, "the new value answered")
	assert.Equal(t, before, staleServed(), "a successful read is not a stale serve")
}

func TestEntitlementsLimitService_AtTwoAndAHalfTTLAFailingReaderInsideTheBoundFallsBackToTheHeldValueAndCountsIt(t *testing.T) {
	reader := newGatedReader(80)
	h := newHarness(t, reader)
	tenant := uuid.New()
	h.prime(t, tenant)
	reader.set(func(r *gatedReader) { r.err = errors.New("entitlements is down") })
	h.clock.advance(testTTL * 5 / 2)
	before := staleServed()

	got, err := h.limitOf(context.Background(), tenant)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(80), *got, "the held value is served when the read failed and it is within the bound")
	assert.Equal(t, 2, reader.callsFor(tenant), "the caller waited for a read first")
	assert.Equal(t, before+1, staleServed())
	assert.Len(t, h.log.waitFor(t, "warn", 1), 1)

	_, err = h.limitOf(context.Background(), tenant)
	require.NoError(t, err, "inside the backoff the held value is served without a new read")
	assert.Equal(t, 2, reader.callsFor(tenant))
	assert.Equal(t, before+2, staleServed())
}

func TestEntitlementsLimitService_AHeldUnlimitedValueIsTheFallbackToo(t *testing.T) {
	reader := newGatedReader(80)
	reader.limit = nil
	h := newHarness(t, reader)
	tenant := uuid.New()
	got, err := h.limitOf(context.Background(), tenant)
	require.NoError(t, err)
	require.Nil(t, got)
	h.settled(t)
	reader.set(func(r *gatedReader) { r.err = errors.New("entitlements is down") })
	h.clock.advance(testTTL * 5 / 2)

	got, err = h.limitOf(context.Background(), tenant)

	require.NoError(t, err)
	assert.Nil(t, got, "unlimited stays unlimited")
}

func TestEntitlementsLimitService_TheWindowEndsAtTheBoundWhenTwiceTheTTLIsBeyondIt(t *testing.T) {
	reader := newGatedReader(80)
	h := newHarness(t, reader)
	svc, err := NewEntitlementsLimitService(EntitlementsLimitDeps{
		Reader: reader, TTL: testTTL, FreshnessBound: testTTL * 3 / 2, Now: h.clock.Now, Rand: h.rnd.next,
	})
	require.NoError(t, err)
	tenant := uuid.New()
	_, err = svc.WorkflowTemplateLimit(context.Background(), tenant, "")
	require.NoError(t, err)
	gate := make(chan struct{})
	reader.set(func(r *gatedReader) { r.gate, r.err = gate, errors.New("down") })
	h.clock.advance(testTTL * 3 / 2)

	result := make(chan error, 1)
	go func() {
		_, err := svc.WorkflowTemplateLimit(context.Background(), tenant, "")
		result <- err
	}()
	require.Eventually(t, func() bool { return reader.callsFor(tenant) == 2 }, 5*time.Second, time.Millisecond)
	close(gate)

	require.ErrorIs(t, <-result, domain.ErrUpstreamUnavailable, "at the bound the held value is no longer served")
}

func TestEntitlementsLimitService_ManyConcurrentCallersStartOneBackgroundRefreshPerTenant(t *testing.T) {
	reader := newGatedReader(5)
	h := newHarness(t, reader)
	tenant, other := uuid.New(), uuid.New()
	h.prime(t, tenant)
	h.prime(t, other)
	gate := make(chan struct{})
	reader.set(func(r *gatedReader) { r.gate = gate })
	h.clock.advance(testTTL)

	var wg sync.WaitGroup
	for i := range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id := tenant
			if i%2 == 1 {
				id = other
			}
			got, err := h.limitOf(context.Background(), id)
			assert.NoError(t, err)
			assert.NotNil(t, got)
		}()
	}
	wg.Wait()
	require.Eventually(t, func() bool { return reader.callsFor(tenant) == 2 && reader.callsFor(other) == 2 }, 5*time.Second, time.Millisecond)
	assert.Equal(t, 2, h.inflightCount())

	close(gate)
	h.settled(t)
	assert.Equal(t, 2, reader.callsFor(tenant), "one refresh for the tenant")
	assert.Equal(t, 2, reader.callsFor(other))
}

func TestEntitlementsLimitService_AFailedBackgroundRefreshKeepsServingTheHeldValueAndTheBackoffBlocksFurtherReads(t *testing.T) {
	reader := newGatedReader(5)
	h := newHarness(t, reader)
	tenant := uuid.New()
	h.prime(t, tenant)
	reader.set(func(r *gatedReader) { r.err = errors.New("entitlements is down") })
	h.clock.advance(testTTL * 3 / 2)

	got, err := h.limitOf(context.Background(), tenant)
	require.NoError(t, err)
	assert.Equal(t, int64(5), *got)
	h.settled(t)
	require.Equal(t, 2, reader.callsFor(tenant))
	warns := h.log.waitFor(t, "warn", 1)
	require.Len(t, warns, 1, "one Warn per failed refresh")
	assert.Equal(t, tenant.String(), warns[0].fields["tenant_id"])
	assert.Contains(t, warns[0].fields["error"], "entitlements is down")

	h.clock.advance(3 * time.Second)
	for range 3 {
		got, err = h.limitOf(context.Background(), tenant)
		require.NoError(t, err)
		assert.Equal(t, int64(5), *got)
		assert.False(t, h.isInflight(tenant), "inside the backoff no read starts")
	}
	assert.Equal(t, 2, reader.callsFor(tenant))
	assert.Len(t, h.log.at("warn"), 1, "serving during the backoff logs nothing")

	h.clock.advance(2 * time.Second)
	_, err = h.limitOf(context.Background(), tenant)
	require.NoError(t, err)
	h.settled(t)
	assert.Equal(t, 3, reader.callsFor(tenant), "after the backoff the next call starts a refresh")
}

func TestEntitlementsLimitService_PastTheBoundTheCallerWaitsForTheReadAndAFailureIsAnError(t *testing.T) {
	reader := newGatedReader(5)
	h := newHarness(t, reader)
	tenant := uuid.New()
	h.prime(t, tenant)
	gate := make(chan struct{})
	reader.set(func(r *gatedReader) { r.gate, r.err = gate, errors.New("entitlements is down") })
	h.clock.advance(testBound)

	result := make(chan error, 1)
	go func() {
		_, err := h.limitOf(context.Background(), tenant)
		result <- err
	}()
	require.Eventually(t, func() bool { return reader.callsFor(tenant) == 2 }, 5*time.Second, time.Millisecond)
	select {
	case err := <-result:
		t.Fatalf("the caller must wait for the read, got %v", err)
	default:
	}

	close(gate)
	err := <-result
	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	assert.ErrorContains(t, err, "entitlements is down")
	assert.Len(t, h.log.waitFor(t, "error", 1), 1, "a read that leaves no usable value is logged at Error")
}

func TestEntitlementsLimitService_PastTheBoundAWaitingCallerHonoursItsOwnContext(t *testing.T) {
	reader := newGatedReader(5)
	h := newHarness(t, reader)
	tenant := uuid.New()
	h.prime(t, tenant)
	gate := make(chan struct{})
	t.Cleanup(func() { close(gate) })
	reader.set(func(r *gatedReader) { r.gate = gate })
	h.clock.advance(testBound)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := h.limitOf(ctx, tenant)
		result <- err
	}()
	require.Eventually(t, func() bool { return h.isInflight(tenant) }, 5*time.Second, time.Millisecond)

	cancel()

	err := <-result
	require.ErrorIs(t, err, context.Canceled)
	assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
}

func TestEntitlementsLimitService_ARunningReadIsNotCancelledWithTheCallerThatStartedIt(t *testing.T) {
	for name, stale := range map[string]bool{"a blocking read": false, "a background refresh": true} {
		t.Run(name, func(t *testing.T) {
			reader := newGatedReader(5)
			h := newHarness(t, reader)
			tenant := uuid.New()
			if stale {
				h.prime(t, tenant)
				h.clock.advance(testTTL)
			}
			gate := make(chan struct{})
			reader.set(func(r *gatedReader) { r.gate, r.ctxErr = gate, true })
			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() {
				_, err := h.limitOf(ctx, tenant)
				result <- err
			}()
			want := 1
			if stale {
				want = 2
			}
			require.Eventually(t, func() bool { return reader.callsFor(tenant) == want }, 5*time.Second, time.Millisecond)

			cancel()
			if !stale {
				require.ErrorIs(t, <-result, context.Canceled)
			} else {
				require.NoError(t, <-result)
			}
			close(gate)
			h.settled(t)

			got, err := h.limitOf(context.Background(), tenant)
			require.NoError(t, err, "the read the cancelled caller started still produced the value")
			assert.Equal(t, int64(5), *got)
			assert.Equal(t, want, reader.callsFor(tenant))
			assert.Empty(t, h.log.at("warn"))
		})
	}
}

func TestEntitlementsLimitService_ConcurrentBlockingCallersShareOneReadAndAGateReleasesThemAll(t *testing.T) {
	reader := newGatedReader(80)
	gate := make(chan struct{})
	reader.gate = gate
	h := newHarness(t, reader)
	tenant := uuid.New()

	const callers = 25
	var wg sync.WaitGroup
	results := make(chan int64, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := h.limitOf(context.Background(), tenant)
			if assert.NoError(t, err) && assert.NotNil(t, got) {
				results <- *got
			}
		}()
	}
	require.Eventually(t, func() bool { return h.isInflight(tenant) && reader.activeNow() == 1 }, 5*time.Second, time.Millisecond)
	close(gate)
	wg.Wait()
	close(results)

	n := 0
	for got := range results {
		assert.Equal(t, int64(80), got)
		n++
	}
	assert.Equal(t, callers, n)
	assert.Equal(t, 1, reader.callsFor(tenant), "one in-flight read per tenant")
}

func TestEntitlementsLimitService_NeverMoreThan32ReadsAreInFlight(t *testing.T) {
	reader := newGatedReader(1)
	gate := make(chan struct{})
	reader.gate = gate
	h := newHarness(t, reader)

	const tenants = 100
	var wg sync.WaitGroup
	for range tenants {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := h.limitOf(context.Background(), uuid.New())
			assert.NoError(t, err)
			assert.NotNil(t, got)
		}()
	}
	require.Eventually(t, func() bool { return reader.activeNow() == 32 }, 5*time.Second, time.Millisecond)
	assert.Equal(t, 32, entitlementsMaxConcurrentReads)
	require.Eventually(t, func() bool { return h.inflightCount() == tenants }, 5*time.Second, time.Millisecond, "the rest wait for a slot")

	close(gate)
	wg.Wait()
	assert.Equal(t, 32, reader.peakActive())
	require.Eventually(t, func() bool { return len(h.svc.slots) == 0 }, 5*time.Second, time.Millisecond, "every slot was given back")
}

func TestEntitlementsLimitService_ABlockingReadWaitsForASlotWithItsContext(t *testing.T) {
	reader := newGatedReader(1)
	gate := make(chan struct{})
	reader.gate = gate
	h := newHarness(t, reader)
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = h.limitOf(context.Background(), uuid.New())
		}()
	}
	require.Eventually(t, func() bool { return reader.activeNow() == 32 }, 5*time.Second, time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := h.limitOf(ctx, uuid.New())
		result <- err
	}()
	require.Eventually(t, func() bool { return h.inflightCount() == 33 }, 5*time.Second, time.Millisecond)

	cancel()

	require.ErrorIs(t, <-result, context.Canceled)
	close(gate)
	wg.Wait()
}

func TestEntitlementsLimitService_ABackgroundRefreshWithoutAFreeSlotIsSkippedAndRetriedByALaterCall(t *testing.T) {
	reader := newGatedReader(1)
	h := newHarness(t, reader)
	held := uuid.New()
	h.prime(t, held)
	gate := make(chan struct{})
	reader.set(func(r *gatedReader) { r.gate = gate })
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = h.limitOf(context.Background(), uuid.New())
		}()
	}
	require.Eventually(t, func() bool { return reader.activeNow() == 32 }, 5*time.Second, time.Millisecond)
	h.clock.advance(testTTL)

	got, err := h.limitOf(context.Background(), held)

	require.NoError(t, err)
	assert.NotNil(t, got, "the held value is served")
	assert.False(t, h.isInflight(held), "no slot, so no refresh was started")
	assert.Equal(t, 1, reader.callsFor(held))

	close(gate)
	wg.Wait()
	h.settled(t)
	_, err = h.limitOf(context.Background(), held)
	require.NoError(t, err)
	h.settled(t)
	assert.Equal(t, 2, reader.callsFor(held), "a later call refreshed it")
}

func TestEntitlementsLimitService_NothingIsLoggedWhileTheCacheLockIsHeld(t *testing.T) {
	reader := newGatedReader(1)
	h := newHarness(t, reader)
	var locked atomic.Bool
	h.log.onLog = func() {
		if h.svc.mu.TryLock() {
			h.svc.mu.Unlock()
			return
		}
		locked.Store(true)
	}
	tenant := uuid.New()
	h.prime(t, tenant)

	reader.set(func(r *gatedReader) { r.err = errors.New("down") })
	h.clock.advance(testTTL * 3 / 2)
	_, err := h.limitOf(context.Background(), tenant)
	require.NoError(t, err)
	// no h.settled here: polling the cache lock would itself make a log call find it held
	require.Len(t, h.log.waitFor(t, "warn", 1), 1, "a failed refresh over a held value logs a Warn")

	_, err = h.limitOf(context.Background(), uuid.New())
	require.Error(t, err)
	require.Len(t, h.log.waitFor(t, "error", 1), 1, "a failed cold read logs an Error")

	reader.set(func(r *gatedReader) { r.panicWith = "boom" })
	_, err = h.limitOf(context.Background(), uuid.New())
	require.Error(t, err)
	require.Len(t, h.log.waitFor(t, "error", 3), 3, "a panic logs its own Error and the failed read's")

	assert.False(t, locked.Load(), "a log call found the cache lock held")
}

func TestEntitlementsLimitService_ARecoveredPanicIsLoggedAtErrorWithItsStackCountedAndFailsTheRead(t *testing.T) {
	reader := newGatedReader(1)
	reader.panicWith = "reader blew up"
	h := newHarness(t, reader)
	tenant := uuid.New()
	before := testutil.ToFloat64(observability.EntitlementsReadPanicsTotal)

	_, err := h.limitOf(context.Background(), tenant)

	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	assert.ErrorContains(t, err, "reader blew up")
	panics := h.log.waitFor(t, "error", 1)[0]
	assert.Equal(t, tenant.String(), panics.fields["tenant_id"])
	assert.Equal(t, "reader blew up", panics.fields["panic"])
	stack, _ := panics.fields["stack"].(string)
	assert.Contains(t, stack, "ReadWorkflowTemplateLimit", "the stack names the code that panicked")
	assert.Equal(t, before+1, testutil.ToFloat64(observability.EntitlementsReadPanicsTotal))

	h.clock.advance(entitlementsRetryBackoff * 2)
	reader.set(func(r *gatedReader) { r.panicWith = nil })
	got, err := h.limitOf(context.Background(), tenant)
	require.NoError(t, err, "a panic is a failed read, not a poisoned tenant")
	assert.Equal(t, int64(1), *got)
}

func TestEntitlementsLimitService_APanicInABackgroundRefreshKeepsTheHeldValueAndTheFailureIsAWarning(t *testing.T) {
	reader := newGatedReader(1)
	h := newHarness(t, reader)
	tenant := uuid.New()
	h.prime(t, tenant)
	reader.set(func(r *gatedReader) { r.panicWith = "boom" })
	h.clock.advance(testTTL * 3 / 2)

	got, err := h.limitOf(context.Background(), tenant)
	require.NoError(t, err)
	assert.NotNil(t, got)
	h.settled(t)

	assert.Len(t, h.log.waitFor(t, "warn", 1), 1)
	assert.Len(t, h.log.waitFor(t, "error", 1), 1, "the panic itself is at Error")
}

func TestEntitlementsLimitService_TheAgeGaugeShowsTheOldestValueServedStaleInTheLastWindow(t *testing.T) {
	reader := newGatedReader(1)
	h := newHarness(t, reader)
	young, old := uuid.New(), uuid.New()
	h.prime(t, old)
	h.clock.advance(3 * time.Minute)
	h.prime(t, young)
	reader.set(func(r *gatedReader) { r.err = errors.New("down") })
	gauge := func() float64 { return testutil.ToFloat64(observability.EntitlementsOldestServedAgeSeconds) }

	h.clock.advance(2 * time.Minute)
	_, err := h.limitOf(context.Background(), young)
	require.NoError(t, err)
	h.settled(t)
	assert.InDelta(t, 120, gauge(), 0.001, "the young value is 2 minutes old")

	_, err = h.limitOf(context.Background(), old)
	require.NoError(t, err)
	assert.InDelta(t, 300, gauge(), 0.001, "the oldest wins")

	_, err = h.limitOf(context.Background(), young)
	require.NoError(t, err)
	assert.InDelta(t, 300, gauge(), 0.001, "a younger serve does not lower it inside the window")

	reader.set(func(r *gatedReader) { r.err = nil })
	h.clock.advance(testTTL)
	_, err = h.limitOf(context.Background(), young)
	require.NoError(t, err)
	h.settled(t)
	h.clock.advance(30 * time.Second)
	fresh := uuid.New()
	h.prime(t, fresh)
	h.clock.advance(31 * time.Second)
	_, err = h.limitOf(context.Background(), fresh)
	require.NoError(t, err)
	assert.Zero(t, gauge(), "a window with no stale serve brings it back to 0")
}

func TestEntitlementsLimitService_ManyTenantsAndCallersUnderFailuresAndClockJumpsStayConsistent(t *testing.T) {
	reader := newGatedReader(7)
	h := newHarness(t, reader)
	tenants := make([]uuid.UUID, 60)
	for i := range tenants {
		tenants[i] = uuid.New()
	}
	var failing atomic.Bool
	h.rnd.set(0.3)
	var wg sync.WaitGroup
	for w := range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(w), 7))
			for i := range 300 {
				switch {
				case w == 0 && i%10 == 0:
					h.clock.advance(time.Duration(rng.IntN(90)) * time.Second)
				case w == 1 && i%25 == 0:
					failing.Store(!failing.Load())
					reader.set(func(r *gatedReader) {
						r.err = nil
						if failing.Load() {
							r.err = errors.New("flaky")
						}
					})
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				got, err := h.limitOf(ctx, tenants[rng.IntN(len(tenants))])
				cancel()
				if err != nil {
					assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
					continue
				}
				if assert.NotNil(t, got) {
					assert.Equal(t, int64(7), *got)
				}
			}
		}()
	}
	wg.Wait()
	h.settled(t)

	require.Eventually(t, func() bool { return len(h.svc.slots) == 0 }, 5*time.Second, time.Millisecond)
	assert.LessOrEqual(t, reader.peakActive(), 32)
}

func ptr(n int64) *int64 { return &n }

var errUpstream = domain.ErrUpstreamUnavailable
