package port

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type TenantPurgeDue struct {
	TenantID      uuid.UUID
	Credentials   bool
	Artifacts     bool
	Attempts      int
	RecordVersion int64
}

type TenantRetentionRepository interface {
	RecordOffboarded(ctx context.Context, tenantID uuid.UUID, offboardedAt, credentialsAfter, artifactsAfter time.Time) error
	ClearOffboarded(ctx context.Context, tenantID uuid.UUID, before time.Time) error
	ListDue(ctx context.Context, now time.Time, limit int) ([]TenantPurgeDue, error)
	MarkCredentialsPurged(ctx context.Context, tenantID uuid.UUID, version int64, at time.Time) (int64, error)
	MarkPurged(ctx context.Context, tenantID uuid.UUID, version int64, at time.Time) (int64, error)
	RecordFailure(ctx context.Context, tenantID uuid.UUID, version int64, attempts int, next time.Time) error
}

type TenantDataPurger interface {
	PurgeBatch(ctx context.Context, tenantID uuid.UUID, limit int) (map[string]int64, error)
	HasRows(ctx context.Context, tenantID uuid.UUID) (bool, error)
}
