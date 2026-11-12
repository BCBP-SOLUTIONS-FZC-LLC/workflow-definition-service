package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const (
	entTTL   = time.Minute
	entBound = 10 * time.Minute
)

type fakeEntitlementsReader struct {
	mu      sync.Mutex
	limits  map[uuid.UUID]*int64
	err     error
	calls   map[uuid.UUID]int
	onRead  func()
	release chan struct{}
	panics  bool
}

func newFakeEntitlementsReader() *fakeEntitlementsReader {
	return &fakeEntitlementsReader{limits: map[uuid.UUID]*int64{}, calls: map[uuid.UUID]int{}}
}

func (r *fakeEntitlementsReader) ReadWorkflowTemplateLimit(ctx context.Context, tenantID uuid.UUID) (*int64, error) {
	r.mu.Lock()
	r.calls[tenantID]++
	limit, err, onRead, release, panics := r.limits[tenantID], r.err, r.onRead, r.release, r.panics
	r.mu.Unlock()
	if onRead != nil {
		onRead()
	}
	if release != nil {
		<-release
	}
	if panics {
		panic("reader exploded")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return limit, err
}

func (r *fakeEntitlementsReader) callsFor(t uuid.UUID) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[t]
}

func (r *fakeEntitlementsReader) set(tenant uuid.UUID, limit *int64, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.limits[tenant] = limit
	r.err = err
}

// entClock is a clock the background reads can consult while the test moves it.
type entClock struct {
	mu  sync.Mutex
	now time.Time
}

func newEntClock() *entClock { return &entClock{now: time.Unix(1000, 0)} }

func (c *entClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *entClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newEntitlements builds the cache with no jitter (a draw of 0.5 is a factor of exactly 1).
func newEntitlements(t *testing.T, reader port.EntitlementsReader, clock *entClock, log port.Logger) *service.EntitlementsLimitService {
	t.Helper()
	svc, err := service.NewEntitlementsLimitService(service.EntitlementsLimitDeps{
		Reader: reader, TTL: entTTL, FreshnessBound: entBound, Now: clock.Now, Log: log, Rand: func() float64 { return 0.5 },
	})
	require.NoError(t, err)
	return svc
}

// waitCalls waits until a background refresh has called the reader n times for the tenant.
func waitCalls(t *testing.T, reader *fakeEntitlementsReader, tenant uuid.UUID, n int) {
	t.Helper()
	require.Eventually(t, func() bool { return reader.callsFor(tenant) >= n }, 5*time.Second, time.Millisecond)
}

// waitWarnings waits until the log holds n warnings; a refresh logs after it settles.
func waitWarnings(t *testing.T, log *w5aRecordingLog, n int) []w5aWarning {
	t.Helper()
	require.Eventually(t, func() bool { return len(log.recorded()) >= n }, 5*time.Second, time.Millisecond)
	return log.recorded()
}

func readLimit(t *testing.T, svc *service.EntitlementsLimitService, tenant uuid.UUID) *int64 {
	t.Helper()
	got, err := svc.WorkflowTemplateLimit(context.Background(), tenant, "")
	require.NoError(t, err)
	return got
}

func TestEntitlementsLimitService_AFreshEntryIsServedWithoutReadingAgain(t *testing.T) {
	tenant := uuid.New()
	reader := newFakeEntitlementsReader()
	reader.set(tenant, limitOf(80), nil)
	clock := newEntClock()
	svc := newEntitlements(t, reader, clock, nil)

	require.Equal(t, int64(80), *readLimit(t, svc, tenant))
	clock.advance(entTTL - time.Second)
	require.Equal(t, int64(80), *readLimit(t, svc, tenant))

	assert.Equal(t, 1, reader.callsFor(tenant))
}

func TestEntitlementsLimitService_AnEntryPastTheTTLIsServedAtOnceAndRefreshedInTheBackground(t *testing.T) {
	tenant := uuid.New()
	reader := newFakeEntitlementsReader()
	reader.set(tenant, limitOf(80), nil)
	clock := newEntClock()
	svc := newEntitlements(t, reader, clock, nil)
	require.Equal(t, int64(80), *readLimit(t, svc, tenant))

	reader.set(tenant, limitOf(120), nil)
	clock.advance(entTTL)

	assert.Equal(t, int64(80), *readLimit(t, svc, tenant), "the held value answers while the refresh runs")
	waitCalls(t, reader, tenant, 2)
	require.Eventually(t, func() bool { return *readLimit(t, svc, tenant) == 120 }, 5*time.Second, time.Millisecond,
		"an override raised since the last read is picked up once the refresh lands")
	assert.Equal(t, 2, reader.callsFor(tenant))
}

func TestEntitlementsLimitService_ANullLimitIsUnlimitedAndIsCached(t *testing.T) {
	tenant := uuid.New()
	reader := newFakeEntitlementsReader()
	reader.set(tenant, nil, nil)
	svc := newEntitlements(t, reader, newEntClock(), nil)

	assert.Nil(t, readLimit(t, svc, tenant))
	assert.Nil(t, readLimit(t, svc, tenant))

	assert.Equal(t, 1, reader.callsFor(tenant), "a nil limit is a value, not a miss")
}

func TestEntitlementsLimitService_ThePlanCodeIsIgnored(t *testing.T) {
	tenant := uuid.New()
	reader := newFakeEntitlementsReader()
	reader.set(tenant, limitOf(5), nil)
	svc := newEntitlements(t, reader, newEntClock(), nil)

	for _, code := range []string{"", "  ", "starter", "no-such-plan"} {
		got, err := svc.WorkflowTemplateLimit(context.Background(), tenant, code)
		require.NoError(t, err, "plan code %q", code)
		require.NotNil(t, got)
		assert.Equal(t, int64(5), *got)
	}
	assert.Equal(t, 1, reader.callsFor(tenant))
}

func TestEntitlementsLimitService_TwoTenantsDoNotShareEntries(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	reader := newFakeEntitlementsReader()
	reader.set(a, limitOf(5), nil)
	reader.set(b, limitOf(500), nil)
	svc := newEntitlements(t, reader, newEntClock(), nil)

	assert.Equal(t, int64(5), *readLimit(t, svc, a))
	assert.Equal(t, int64(500), *readLimit(t, svc, b))
	assert.Equal(t, int64(5), *readLimit(t, svc, a))

	assert.Equal(t, 1, reader.callsFor(a))
	assert.Equal(t, 1, reader.callsFor(b))
}

func TestEntitlementsLimitService_AFailedRefreshInsideTheBoundServesTheHeldValueAndWarns(t *testing.T) {
	tenant := uuid.New()
	reader := newFakeEntitlementsReader()
	reader.set(tenant, limitOf(80), nil)
	clock := newEntClock()
	log := &w5aRecordingLog{}
	svc := newEntitlements(t, reader, clock, log)
	require.Equal(t, int64(80), *readLimit(t, svc, tenant))

	reader.set(tenant, nil, errors.New("entitlements down"))
	clock.advance(entBound - time.Second)

	assert.Equal(t, int64(80), *readLimit(t, svc, tenant))
	waitCalls(t, reader, tenant, 2)
	warnings := waitWarnings(t, log, 1)
	require.Len(t, warnings, 1, "one warning for the one failed refresh")
	assert.Equal(t, tenant.String(), warnings[0].fields["tenant_id"])
	assert.Contains(t, warnings[0].fields["error"], "entitlements down")
	assert.Equal(t, 2, reader.callsFor(tenant))
}

func TestEntitlementsLimitService_AFailedRefreshPastTheBoundIsUnavailable(t *testing.T) {
	tenant := uuid.New()
	reader := newFakeEntitlementsReader()
	reader.set(tenant, limitOf(80), nil)
	clock := newEntClock()
	svc := newEntitlements(t, reader, clock, nil)
	require.Equal(t, int64(80), *readLimit(t, svc, tenant))

	reader.set(tenant, nil, errors.New("entitlements down"))
	clock.advance(entBound)

	got, err := svc.WorkflowTemplateLimit(context.Background(), tenant, "")
	assert.Nil(t, got)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	assert.Contains(t, err.Error(), "entitlements down")
}

func TestEntitlementsLimitService_ANeverCachedTenantWithAFailingReadIsUnavailableAndNothingIsCached(t *testing.T) {
	tenant := uuid.New()
	reader := newFakeEntitlementsReader()
	reader.set(tenant, nil, errors.New("entitlements down"))
	clock := newEntClock()
	svc := newEntitlements(t, reader, clock, nil)

	_, err := svc.WorkflowTemplateLimit(context.Background(), tenant, "")
	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)

	reader.set(tenant, limitOf(7), nil)
	clock.advance(5 * time.Second)

	assert.Equal(t, int64(7), *readLimit(t, svc, tenant), "the failure was not kept as a limit; the next read after the backoff succeeds")
}

func TestEntitlementsLimitService_AfterAFailedReadTheReaderIsNotCalledAgainFor5Seconds(t *testing.T) {
	tenant := uuid.New()
	reader := newFakeEntitlementsReader()
	reader.set(tenant, limitOf(80), nil)
	clock := newEntClock()
	log := &w5aRecordingLog{}
	svc := newEntitlements(t, reader, clock, log)
	require.Equal(t, int64(80), *readLimit(t, svc, tenant))

	reader.set(tenant, nil, errors.New("entitlements down"))
	clock.advance(entTTL)
	require.Equal(t, int64(80), *readLimit(t, svc, tenant))
	waitCalls(t, reader, tenant, 2)
	waitWarnings(t, log, 1)

	for _, wait := range []time.Duration{0, time.Second, 3 * time.Second} {
		clock.advance(wait)
		assert.Equal(t, int64(80), *readLimit(t, svc, tenant), "the held value keeps answering during the backoff")
	}
	assert.Equal(t, 2, reader.callsFor(tenant), "no read inside the backoff")

	reader.set(tenant, limitOf(90), nil)
	clock.advance(time.Second)
	assert.Equal(t, int64(80), *readLimit(t, svc, tenant), "the call that ends the backoff still gets the held value")
	waitCalls(t, reader, tenant, 3)
	require.Eventually(t, func() bool { return *readLimit(t, svc, tenant) == 90 }, 5*time.Second, time.Millisecond)
	assert.Equal(t, 3, reader.callsFor(tenant))
}

func TestEntitlementsLimitService_DuringTheBackoffAnEntryPastTheBoundFailsAtOnce(t *testing.T) {
	tenant := uuid.New()
	reader := newFakeEntitlementsReader()
	reader.set(tenant, nil, errors.New("entitlements down"))
	clock := newEntClock()
	svc := newEntitlements(t, reader, clock, nil)

	_, err := svc.WorkflowTemplateLimit(context.Background(), tenant, "")
	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	require.Equal(t, 1, reader.callsFor(tenant))

	clock.advance(2 * time.Second)
	_, err = svc.WorkflowTemplateLimit(context.Background(), tenant, "")

	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	assert.Contains(t, err.Error(), "entitlements down", "the last failure is named")
	assert.Equal(t, 1, reader.callsFor(tenant), "no second read inside the backoff")
}

func TestEntitlementsLimitService_TheBackoffIsPerTenant(t *testing.T) {
	down, up := uuid.New(), uuid.New()
	reader := newFakeEntitlementsReader()
	reader.set(up, limitOf(3), nil)
	clock := newEntClock()
	svc := newEntitlements(t, reader, clock, nil)

	reader.mu.Lock()
	reader.err = errors.New("down for everyone")
	reader.mu.Unlock()
	_, err := svc.WorkflowTemplateLimit(context.Background(), down, "")
	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	reader.mu.Lock()
	reader.err = nil
	reader.mu.Unlock()

	assert.Equal(t, int64(3), *readLimit(t, svc, up), "another tenant is not held back by this one's failure")
}

func TestEntitlementsLimitService_ConcurrentCallersForOneTenantShareOneRead(t *testing.T) {
	tenant := uuid.New()
	reader := newFakeEntitlementsReader()
	reader.set(tenant, limitOf(80), nil)
	reader.release = make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	reader.onRead = func() { once.Do(func() { close(started) }) }
	svc := newEntitlements(t, reader, newEntClock(), nil)

	const callers = 25
	results := make(chan int64, callers)
	errs := make(chan error, callers)
	var wg, ready sync.WaitGroup
	for range callers {
		wg.Add(1)
		ready.Add(1)
		go func() {
			defer wg.Done()
			ready.Done()
			got, err := svc.WorkflowTemplateLimit(context.Background(), tenant, "")
			if err != nil {
				errs <- err
				return
			}
			results <- *got
		}()
	}
	<-started
	ready.Wait() // every caller is running; the read is held open by the gate until they are
	close(reader.release)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Fatalf("unexpected error: %v", err)
	}
	n := 0
	for got := range results {
		assert.Equal(t, int64(80), got)
		n++
	}
	assert.Equal(t, callers, n)
	assert.Equal(t, 1, reader.callsFor(tenant), "one in-flight read per tenant")
}

func TestEntitlementsLimitService_ConcurrentCallersForDifferentTenantsReadSeparately(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	reader := newFakeEntitlementsReader()
	reader.set(a, limitOf(1), nil)
	reader.set(b, limitOf(2), nil)
	svc := newEntitlements(t, reader, newEntClock(), nil)

	var wg sync.WaitGroup
	for _, tenant := range []uuid.UUID{a, b, a, b, a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = svc.WorkflowTemplateLimit(context.Background(), tenant, "")
		}()
	}
	wg.Wait()

	assert.Equal(t, int64(1), *readLimit(t, svc, a))
	assert.Equal(t, int64(2), *readLimit(t, svc, b))
	assert.Equal(t, 1, reader.callsFor(a), "six callers over two tenants read each tenant exactly once")
	assert.Equal(t, 1, reader.callsFor(b))
}

func TestEntitlementsLimitService_ACallerCancelledWhileWaitingFailsAloneAndTheReadStillFillsTheEntry(t *testing.T) {
	tenant := uuid.New()
	reader := newFakeEntitlementsReader()
	reader.set(tenant, limitOf(80), nil)
	reader.release = make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	reader.onRead = func() { once.Do(func() { close(started) }) }
	svc := newEntitlements(t, reader, newEntClock(), nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.WorkflowTemplateLimit(ctx, tenant, "")
		done <- err
	}()
	<-started
	cancel()
	err := <-done
	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	require.ErrorIs(t, err, context.Canceled)

	close(reader.release)
	require.Eventually(t, func() bool {
		got, err := svc.WorkflowTemplateLimit(context.Background(), tenant, "")
		return err == nil && got != nil && *got == 80
	}, time.Second, 5*time.Millisecond, "the read ran on without the cancelled caller's context")
	assert.Equal(t, 1, reader.callsFor(tenant))
}

func TestEntitlementsLimitService_APanickingReaderIsAnUnavailableErrorNotACrash(t *testing.T) {
	tenant := uuid.New()
	reader := newFakeEntitlementsReader()
	reader.panics = true
	clock := newEntClock()
	svc := newEntitlements(t, reader, clock, nil)

	_, err := svc.WorkflowTemplateLimit(context.Background(), tenant, "")
	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)

	reader.mu.Lock()
	reader.panics = false
	reader.limits[tenant] = limitOf(4)
	reader.mu.Unlock()
	clock.advance(5 * time.Second)
	assert.Equal(t, int64(4), *readLimit(t, svc, tenant))
}

func TestEntitlementsLimitService_ANilLoggerAndNilClockAreAccepted(t *testing.T) {
	tenant := uuid.New()
	reader := newFakeEntitlementsReader()
	reader.set(tenant, limitOf(2), nil)
	svc, err := service.NewEntitlementsLimitService(service.EntitlementsLimitDeps{Reader: reader, TTL: entTTL, FreshnessBound: entBound})
	require.NoError(t, err)

	assert.Equal(t, int64(2), *readLimit(t, svc, tenant))
}
