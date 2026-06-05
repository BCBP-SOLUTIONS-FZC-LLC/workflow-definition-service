package postgres_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
)

func TestProcessedEventRepo_RecordIfNew(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewProcessedEventRepo(pool)
	ctx := context.Background()

	eventID := uuid.New()
	tenantID := uuid.New()

	isNew, err := repo.RecordIfNew(ctx, eventID, tenantID, "iam.membership.events")
	if err != nil {
		t.Fatalf("first RecordIfNew: %v", err)
	}
	if !isNew {
		t.Error("first call should return isNew=true")
	}

	isNew, err = repo.RecordIfNew(ctx, eventID, tenantID, "iam.membership.events")
	if err != nil {
		t.Fatalf("second RecordIfNew: %v", err)
	}
	if isNew {
		t.Error("second call should return isNew=false (idempotent)")
	}
}

func TestProcessedEventRepo_PruneOlderThan(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewProcessedEventRepo(pool)
	ctx := context.Background()

	for range 2 {
		if _, err := repo.RecordIfNew(ctx, uuid.New(), uuid.New(), "test"); err != nil {
			t.Fatalf("RecordIfNew: %v", err)
		}
	}

	if err := repo.PruneOlderThan(ctx, 0); err != nil {
		t.Fatalf("PruneOlderThan: %v", err)
	}
}
