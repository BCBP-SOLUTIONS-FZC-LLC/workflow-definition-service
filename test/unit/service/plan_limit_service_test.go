package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type fakePlanTableReader struct {
	mu      sync.Mutex
	table   map[string]*int64
	err     error
	calls   int
	onRead  func()
	release chan struct{}
}

func (r *fakePlanTableReader) ReadPlanTable(ctx context.Context) (map[string]*int64, error) {
	r.mu.Lock()
	r.calls++
	table, err, onRead, release := r.table, r.err, r.onRead, r.release
	r.mu.Unlock()
	if onRead != nil {
		onRead()
	}
	if release != nil {
		<-release
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	return table, nil
}

func (r *fakePlanTableReader) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func limitOf(n int64) *int64 { return &n }

type planClock struct{ now time.Time }

func (c *planClock) Now() time.Time { return c.now }

func newPlanLimits(reader *fakePlanTableReader, clock *planClock) *service.PlanLimitService {
	return service.NewPlanLimitService(service.PlanLimitDeps{Reader: reader, TTL: time.Minute, Now: clock.Now})
}

func TestPlanLimitService_LooksUpTheLimitByPlanCode(t *testing.T) {
	reader := &fakePlanTableReader{table: map[string]*int64{"starter": limitOf(5), "pro": limitOf(50), "enterprise": nil, "frozen": limitOf(0)}}
	svc := newPlanLimits(reader, &planClock{now: time.Unix(0, 0)})

	got, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(5), *got)

	got, err = svc.WorkflowTemplateLimit(context.Background(), " PRO ")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(50), *got, "the code is trimmed and lower-cased")

	got, err = svc.WorkflowTemplateLimit(context.Background(), "enterprise")
	require.NoError(t, err)
	assert.Nil(t, got, "a null limit is unlimited")

	got, err = svc.WorkflowTemplateLimit(context.Background(), "frozen")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(0), *got, "zero is a hard cap, not unlimited")
	assert.Equal(t, 1, reader.callCount(), "one read serves every lookup inside the freshness window")
}

func TestPlanLimitService_AnUnknownOrMissingPlanCodeIsUnavailableNeverADefault(t *testing.T) {
	reader := &fakePlanTableReader{table: map[string]*int64{"starter": limitOf(5)}}
	svc := newPlanLimits(reader, &planClock{now: time.Unix(0, 0)})

	for _, code := range []string{"", "   ", "platinum"} {
		_, err := svc.WorkflowTemplateLimit(context.Background(), code)
		assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable, "code %q", code)
	}
}

func TestPlanLimitService_NoTableEverReadIsUnavailable(t *testing.T) {
	reader := &fakePlanTableReader{err: errors.New("catalog down")}
	svc := newPlanLimits(reader, &planClock{now: time.Unix(0, 0)})

	_, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
	assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
}

func TestPlanLimitService_RefreshesAfterTheFreshnessWindow(t *testing.T) {
	reader := &fakePlanTableReader{table: map[string]*int64{"starter": limitOf(5)}}
	clock := &planClock{now: time.Unix(0, 0)}
	svc := newPlanLimits(reader, clock)

	_, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
	require.NoError(t, err)
	reader.table = map[string]*int64{"starter": limitOf(8)}
	clock.now = clock.now.Add(2 * time.Minute)

	assert.Eventually(t, func() bool {
		got, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
		return err == nil && got != nil && *got == 8
	}, 2*time.Second, 5*time.Millisecond)
	assert.Equal(t, 2, reader.callCount())
}

func TestPlanLimitService_KeepsTheLastTableWhenARefreshFails(t *testing.T) {
	reader := &fakePlanTableReader{table: map[string]*int64{"starter": limitOf(5)}}
	clock := &planClock{now: time.Unix(0, 0)}
	svc := newPlanLimits(reader, clock)

	_, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
	require.NoError(t, err)
	reader.err = errors.New("catalog down")
	clock.now = clock.now.Add(time.Hour)

	got, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(5), *got, "the last table read still answers")
}

func TestPlanLimitService_DoesNotRetryTheCatalogOnEveryRequestDuringAnOutage(t *testing.T) {
	reader := &fakePlanTableReader{err: errors.New("catalog down")}
	clock := &planClock{now: time.Unix(0, 0)}
	svc := newPlanLimits(reader, clock)

	for range 5 {
		_, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
		assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	}
	assert.Equal(t, 1, reader.callCount(), "failures inside the retry backoff do not reach the catalog")

	clock.now = clock.now.Add(10 * time.Second)
	reader.err = nil
	reader.table = map[string]*int64{"starter": limitOf(5)}
	got, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, 2, reader.callCount())
}

func TestPlanLimitService_AFailedRefreshIsNotRetriedInsideTheBackoffEither(t *testing.T) {
	reader := &fakePlanTableReader{table: map[string]*int64{"starter": limitOf(5)}}
	clock := &planClock{now: time.Unix(0, 0)}
	svc := newPlanLimits(reader, clock)
	_, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
	require.NoError(t, err)

	reader.err = errors.New("catalog down")
	clock.now = clock.now.Add(time.Hour)
	for range 5 {
		_, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
		require.NoError(t, err)
	}
	assert.Eventually(t, func() bool { return reader.callCount() == 2 }, 2*time.Second, 5*time.Millisecond, "one failed refresh")
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 2, reader.callCount(), "then the backoff holds")
}

func TestPlanLimitService_AnExpiredTableIsServedWhileTheRefreshRuns(t *testing.T) {
	reader := &fakePlanTableReader{table: map[string]*int64{"starter": limitOf(5)}}
	clock := &planClock{now: time.Unix(0, 0)}
	svc := newPlanLimits(reader, clock)
	_, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
	require.NoError(t, err)

	release := make(chan struct{})
	reader.mu.Lock()
	reader.release = release
	reader.table = map[string]*int64{"starter": limitOf(8)}
	reader.mu.Unlock()
	clock.now = clock.now.Add(2 * time.Minute)

	answered := make(chan *int64, 1)
	go func() {
		got, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
		if err != nil {
			answered <- nil
			return
		}
		answered <- got
	}()
	select {
	case got := <-answered:
		require.NotNil(t, got)
		assert.Equal(t, int64(5), *got, "the held table answers while the catalog is slow")
	case <-time.After(time.Second):
		t.Fatal("the request that found the table expired waited for the catalog")
	}

	close(release)
	assert.Eventually(t, func() bool {
		got, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
		return err == nil && got != nil && *got == 8
	}, 2*time.Second, 5*time.Millisecond, "the refresh lands for later requests")
}

func TestPlanLimitService_APanickingReaderDoesNotWedgeTheRefresh(t *testing.T) {
	var once sync.Once
	reader := &fakePlanTableReader{table: map[string]*int64{"starter": limitOf(5)}}
	reader.onRead = func() { once.Do(func() { panic("reader bug") }) }
	clock := &planClock{now: time.Unix(0, 0)}
	svc := newPlanLimits(reader, clock)

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("a reader panic escaped: %v", r)
			}
		}()
		_, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
		assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	}()

	clock.now = clock.now.Add(10 * time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	got, err := svc.WorkflowTemplateLimit(ctx, "starter")
	require.NoError(t, err, "the next read after the backoff must not wait on the panicked one")
	require.NotNil(t, got)
	assert.Equal(t, int64(5), *got)
}

type staticLimits map[string]*int64

func (l staticLimits) WorkflowTemplateLimit(_ context.Context, planCode string) (*int64, error) {
	limit, ok := l[planCode]
	if !ok {
		return nil, domain.ErrUpstreamUnavailable
	}
	return limit, nil
}

func stdPlanLimits() port.PlanLimits {
	return staticLimits{"starter": limitOf(5), "pro": limitOf(50), "enterprise": nil}
}

func TestPlanLimitService_ConcurrentCallersShareOneRead(t *testing.T) {
	reader := &fakePlanTableReader{table: map[string]*int64{"starter": limitOf(5)}, release: make(chan struct{})}
	svc := newPlanLimits(reader, &planClock{now: time.Unix(0, 0)})

	const callers = 20
	results := make(chan error, callers)
	for range callers {
		go func() {
			_, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
			results <- err
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(reader.release)
	for range callers {
		require.NoError(t, <-results)
	}
	assert.Equal(t, 1, reader.callCount(), "callers that arrive during a read wait for it instead of starting their own")
}

func TestPlanLimitService_ACallerIsNotKeptWaitingByARefreshWhenATableIsHeld(t *testing.T) {
	reader := &fakePlanTableReader{table: map[string]*int64{"starter": limitOf(5)}}
	clock := &planClock{now: time.Unix(0, 0)}
	svc := newPlanLimits(reader, clock)
	_, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
	require.NoError(t, err)

	reader.mu.Lock()
	reader.release = make(chan struct{})
	reader.mu.Unlock()
	clock.now = clock.now.Add(time.Hour)
	go func() { _, _ = svc.WorkflowTemplateLimit(context.Background(), "starter") }()
	time.Sleep(50 * time.Millisecond)

	done := make(chan struct{})
	go func() {
		got, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
		assert.NoError(t, err)
		assert.NotNil(t, got)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a caller waited on a refresh although a table was held")
	}
	close(reader.release)
}

func TestPlanLimitService_ACancelledCallerDoesNotFailTheReadForOthers(t *testing.T) {
	reader := &fakePlanTableReader{table: map[string]*int64{"starter": limitOf(5)}}
	svc := newPlanLimits(reader, &planClock{now: time.Unix(0, 0)})

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = svc.WorkflowTemplateLimit(cancelled, "starter")

	got, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
	require.NoError(t, err, "the shared read does not run under one caller's context")
	require.NotNil(t, got)
}

func TestPlanLimitService_TheBackoffRunsFromTheEndOfAFailedRead(t *testing.T) {
	clock := &planClock{now: time.Unix(0, 0)}
	reader := &fakePlanTableReader{err: errors.New("catalog down"), onRead: func() { clock.now = clock.now.Add(5 * time.Second) }}
	svc := newPlanLimits(reader, clock)

	_, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
	require.Error(t, err)
	_, err = svc.WorkflowTemplateLimit(context.Background(), "starter")
	require.Error(t, err)
	assert.Equal(t, 1, reader.callCount(), "a read that took as long as the backoff must not be retried at once")
}

func TestPlanLimitService_APlanAddedAfterTheLastReadIsFoundByOneRereadNotAfterTheTTL(t *testing.T) {
	reader := &fakePlanTableReader{table: map[string]*int64{"starter": limitOf(5)}}
	clock := &planClock{now: time.Unix(0, 0)}
	svc := newPlanLimits(reader, clock)
	_, err := svc.WorkflowTemplateLimit(context.Background(), "starter")
	require.NoError(t, err)

	reader.mu.Lock()
	reader.table = map[string]*int64{"starter": limitOf(5), "team": limitOf(20)}
	reader.mu.Unlock()
	_, err = svc.WorkflowTemplateLimit(context.Background(), "team")
	assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable, "a miss right after a read does not hit the catalog again")
	assert.Equal(t, 1, reader.callCount())

	clock.now = clock.now.Add(10 * time.Second)
	got, err := svc.WorkflowTemplateLimit(context.Background(), "team")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(20), *got)
	assert.Equal(t, 2, reader.callCount())
}
