package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestStarterRepo_GetByID_ClosedPoolIsUnavailableNotNotFound(t *testing.T) {
	pool := newTestPool(t)
	repo := postgres.NewStarterRepo(pool)
	pool.Close()

	_, err := repo.GetByID(context.Background(), uuid.New(), uuid.New())

	if !errors.Is(err, domain.ErrUpstreamUnavailable) {
		t.Errorf("GetByID on a closed pool = %v, want ErrUpstreamUnavailable so the read answers 503", err)
	}
	if errors.Is(err, domain.ErrNotFound) {
		t.Errorf("a closed pool must not be reported as not found: %v", err)
	}
}
