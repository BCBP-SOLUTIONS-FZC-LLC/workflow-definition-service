package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestTransactor_RollbackOnError(t *testing.T) {
	pool := newTestPool(t)
	tx := postgres.NewTransactor(pool)
	wfRepo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "tx-rollback", Name: "X",
	}

	sentinel := errors.New("force rollback")
	err := tx.RunInTx(ctx, func(txCtx context.Context) error {
		if err := wfRepo.Create(txCtx, wf); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("RunInTx returned %v, want sentinel", err)
	}

	// Workflow must not exist — the transaction was rolled back.
	_, err = wfRepo.GetByID(ctx, tenantID, wf.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("after rollback: GetByID = %v, want ErrNotFound", err)
	}
}

func TestTransactor_CommitOnSuccess(t *testing.T) {
	pool := newTestPool(t)
	tx := postgres.NewTransactor(pool)
	wfRepo := postgres.NewWorkflowRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), BusinessKey: "tx-commit", Name: "X",
	}

	if err := tx.RunInTx(ctx, func(txCtx context.Context) error {
		return wfRepo.Create(txCtx, wf)
	}); err != nil {
		t.Fatalf("RunInTx: %v", err)
	}

	got, err := wfRepo.GetByID(ctx, tenantID, wf.ID)
	if err != nil {
		t.Fatalf("GetByID after commit: %v", err)
	}
	if got.ID != wf.ID {
		t.Errorf("ID = %v, want %v", got.ID, wf.ID)
	}
}
