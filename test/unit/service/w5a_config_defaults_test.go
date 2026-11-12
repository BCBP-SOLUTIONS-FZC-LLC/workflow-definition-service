package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestTenantOffboardingService_WithoutAClock_AnEventWithoutATimeIsAnchoredAtReceiptOnTheRealClock(t *testing.T) {
	r := &fakeRetention{}
	svc := service.NewTenantOffboardingService(service.TenantOffboardingDeps{
		Retention: r, CredentialRetention: 72 * time.Hour, ArtifactRetention: 240 * time.Hour,
	})
	tenant := uuid.New()

	before := time.Now()
	require.NoError(t, svc.RecordOffboarded(context.Background(), tenant, time.Time{}))
	after := time.Now()

	require.Len(t, r.recorded, 1)
	got := r.recorded[0]
	assert.Equal(t, tenant, got.tenantID)
	assert.False(t, got.offboardedAt.Before(before), "the anchor is not earlier than the call")
	assert.False(t, got.offboardedAt.After(after), "the anchor is not later than the call")
	assert.Equal(t, got.offboardedAt.Add(72*time.Hour), got.credentialsAfter)
	assert.Equal(t, got.offboardedAt.Add(240*time.Hour), got.artifactsAfter)
}

func TestTenantPurger_WithoutALogger_AFailingTenantIsReportedWithoutPanicking(t *testing.T) {
	tenant := uuid.New()
	cause := errors.New("store unavailable")
	r := &fakeRetention{due: bothDue(tenant)}
	failed := 0
	p := service.NewTenantPurger(service.TenantPurgerDeps{
		Retention: r, Data: &fakeDataPurger{errFor: map[uuid.UUID]error{tenant: cause}},
		Batch: 10, Interval: time.Hour,
		Record: func(string, int64) {}, Failed: func() { failed++ },
	})

	var err error
	require.NotPanics(t, func() { err = p.Tick(context.Background()) })

	require.Error(t, err)
	assert.ErrorIs(t, err, cause)
	assert.Equal(t, 1, failed)
	require.Len(t, r.failures, 1)
	assert.Equal(t, tenant, r.failures[0].tenantID)
}
