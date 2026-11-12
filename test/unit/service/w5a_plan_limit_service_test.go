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
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestPlanLimitService_ABlankPlanCodeIsUnavailableWithoutReadingTheCatalog(t *testing.T) {
	cases := []struct {
		name string
		code string
	}{
		{name: "empty", code: ""},
		{name: "spaces", code: "   "},
		{name: "tab and newline", code: "\t\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reader := &fakePlanTableReader{table: map[string]*int64{"starter": limitOf(5)}}
			svc := newPlanLimits(reader, &planClock{now: time.Unix(0, 0)})

			got, err := svc.WorkflowTemplateLimit(context.Background(), uuid.Nil, tc.code)

			assert.Nil(t, got)
			assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
			assert.Equal(t, 0, reader.callCount(), "a blank code is refused before the plan table is touched")
		})
	}
}

func TestPlanLimitService_ACodeWithPaddingAndCapitalsIsTrimmedBeforeTheTableIsRead(t *testing.T) {
	reader := &fakePlanTableReader{table: map[string]*int64{"pro": limitOf(50)}}
	svc := newPlanLimits(reader, &planClock{now: time.Unix(0, 0)})

	got, err := svc.WorkflowTemplateLimit(context.Background(), uuid.Nil, "  PrO\t")

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(50), *got)
	assert.Equal(t, 1, reader.callCount())
}

type w5aRecordingLog struct {
	mu    sync.Mutex
	warns []w5aWarning
}

func (l *w5aRecordingLog) Info(string, map[string]any)  {}
func (l *w5aRecordingLog) Error(string, map[string]any) {}
func (l *w5aRecordingLog) Fatal(string, map[string]any) {}
func (l *w5aRecordingLog) Debug(string, map[string]any) {}
func (l *w5aRecordingLog) Warn(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, w5aWarning{msg: msg, fields: fields})
}

func (l *w5aRecordingLog) recorded() []w5aWarning {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]w5aWarning(nil), l.warns...)
}

func TestPlanLimitService_AFailedForcedRereadForAnUnknownPlan_IsUnavailableAndTheHeldTableKeepsAnswering(t *testing.T) {
	reader := &fakePlanTableReader{table: map[string]*int64{"starter": limitOf(5)}}
	clock := &planClock{now: time.Unix(0, 0)}
	log := &w5aRecordingLog{}
	svc := service.NewPlanLimitService(service.PlanLimitDeps{Reader: reader, TTL: time.Minute, Now: clock.Now, Log: log})
	_, err := svc.WorkflowTemplateLimit(context.Background(), uuid.Nil, "starter")
	require.NoError(t, err)

	reader.mu.Lock()
	reader.err = errors.New("catalog down")
	reader.mu.Unlock()
	clock.now = clock.now.Add(10 * time.Second)

	got, err := svc.WorkflowTemplateLimit(context.Background(), uuid.Nil, "platinum")

	assert.Nil(t, got)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	assert.Equal(t, 2, reader.callCount(), "the unknown plan forced one re-read")
	warnings := log.recorded()
	require.Len(t, warnings, 1)
	assert.Equal(t, "plan table refresh failed, keeping the last table", warnings[0].msg)
	assert.Equal(t, "catalog down", warnings[0].fields["error"])

	held, err := svc.WorkflowTemplateLimit(context.Background(), uuid.Nil, "starter")
	require.NoError(t, err)
	require.NotNil(t, held)
	assert.Equal(t, int64(5), *held, "the table held before the failed re-read keeps answering")
}

func TestPlanLimitService_ACallerCancelledWhileTheColdReadRuns_FailsAloneAndTheReadStillFillsTheTable(t *testing.T) {
	started := make(chan struct{})
	var once sync.Once
	reader := &fakePlanTableReader{
		table:   map[string]*int64{"starter": limitOf(5)},
		release: make(chan struct{}),
		onRead:  func() { once.Do(func() { close(started) }) },
	}
	svc := newPlanLimits(reader, &planClock{now: time.Unix(0, 0)})

	ctx, cancel := context.WithCancel(context.Background())
	type outcome struct {
		limit *int64
		err   error
	}
	first := make(chan outcome, 1)
	go func() {
		limit, err := svc.WorkflowTemplateLimit(ctx, uuid.Nil, "starter")
		first <- outcome{limit, err}
	}()
	<-started
	cancel()
	res := <-first

	assert.Nil(t, res.limit)
	require.Error(t, res.err)
	assert.ErrorIs(t, res.err, domain.ErrUpstreamUnavailable)
	assert.ErrorIs(t, res.err, context.Canceled)

	close(reader.release)
	got, err := svc.WorkflowTemplateLimit(context.Background(), uuid.Nil, "starter")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(5), *got)
	assert.Equal(t, 1, reader.callCount(), "the read the cancelled caller started served the next caller")
}

func TestPlanLimitService_ASuccessfulReadAfterFailures_ServesTheNewTableAndKeepsItWhenTheNextRefreshFails(t *testing.T) {
	reader := &fakePlanTableReader{err: errors.New("catalog down")}
	clock := &planClock{now: time.Unix(0, 0)}
	log := &w5aRecordingLog{}
	svc := service.NewPlanLimitService(service.PlanLimitDeps{Reader: reader, TTL: time.Minute, Now: clock.Now, Log: log})

	_, err := svc.WorkflowTemplateLimit(context.Background(), uuid.Nil, "starter")
	require.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	require.Empty(t, log.recorded(), "a failure with no table held has nothing to keep, so it does not warn")

	clock.now = clock.now.Add(10 * time.Second)
	reader.mu.Lock()
	reader.err = nil
	reader.table = map[string]*int64{"starter": limitOf(8)}
	reader.mu.Unlock()
	got, err := svc.WorkflowTemplateLimit(context.Background(), uuid.Nil, "starter")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(8), *got)
	require.Equal(t, 2, reader.callCount())

	clock.now = clock.now.Add(30 * time.Second)
	_, err = svc.WorkflowTemplateLimit(context.Background(), uuid.Nil, "starter")
	require.NoError(t, err)
	assert.Equal(t, 2, reader.callCount(), "the successful read stamped its time, so the table is fresh")

	reader.mu.Lock()
	reader.err = errors.New("catalog down again")
	reader.mu.Unlock()
	clock.now = clock.now.Add(2 * time.Minute)
	got, err = svc.WorkflowTemplateLimit(context.Background(), uuid.Nil, "starter")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(8), *got, "the table from the successful read answers while its refresh fails")
	assert.Eventually(t, func() bool { return len(log.recorded()) == 1 }, 5*time.Second, time.Millisecond)
	assert.Equal(t, 3, reader.callCount(), "one TTL refresh was attempted")
	assert.Equal(t, "catalog down again", log.recorded()[0].fields["error"])
}

func TestPlanLimitService_WithoutNowOrLog_UsesTheRealClockAndKeepsServingTheHeldTableWhenARefreshFails(t *testing.T) {
	reader := &fakePlanTableReader{table: map[string]*int64{"starter": limitOf(5)}}
	svc := service.NewPlanLimitService(service.PlanLimitDeps{Reader: reader, TTL: time.Hour})

	for range 3 {
		got, err := svc.WorkflowTemplateLimit(context.Background(), uuid.Nil, "starter")
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, int64(5), *got)
	}
	assert.Equal(t, 1, reader.callCount(), "a table read just now is fresh on the real clock")

	expiring := &fakePlanTableReader{table: map[string]*int64{"starter": limitOf(5)}}
	noLog := service.NewPlanLimitService(service.PlanLimitDeps{Reader: expiring})
	_, err := noLog.WorkflowTemplateLimit(context.Background(), uuid.Nil, "starter")
	require.NoError(t, err)
	expiring.mu.Lock()
	expiring.err = errors.New("catalog down")
	expiring.mu.Unlock()
	got, err := noLog.WorkflowTemplateLimit(context.Background(), uuid.Nil, "starter")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(5), *got)
	assert.Eventually(t, func() bool { return expiring.callCount() == 2 }, 5*time.Second, time.Millisecond)

	again, err := noLog.WorkflowTemplateLimit(context.Background(), uuid.Nil, "starter")
	require.NoError(t, err)
	require.NotNil(t, again)
	assert.Equal(t, int64(5), *again, "the held table keeps answering once the failed refresh has settled")
	assert.Equal(t, 2, expiring.callCount(), "the failed refresh is not retried inside the backoff")
}
