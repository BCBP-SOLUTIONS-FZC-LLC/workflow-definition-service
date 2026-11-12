//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func versionWriteErrorsPublished(workflowID, tenantID uuid.UUID, number int32) *domain.WorkflowVersion {
	publishedAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	v := newDraftVersion(workflowID, tenantID)
	v.Status = domain.VersionStatusPublished
	v.VersionNumber = &number
	plan := `{"steps":[]}`
	v.CompiledPlanJSON = &plan
	v.ArtifactHash = "hash"
	v.PublishedAt = &publishedAt
	return v
}

func versionWriteErrorsWorkflow(t *testing.T, repo *postgres.WorkflowRepo, tenantID uuid.UUID) *domain.Workflow {
	t.Helper()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), TemplateKey: "wv-write-" + uuid.NewString(), Name: "X",
	}
	if err := repo.Create(context.Background(), wf); err != nil {
		t.Fatalf("create workflow: %v", err)
	}
	return wf
}

func TestWorkflowVersionRepo_Create_SecondPublishedVersionWithTheSameNumber_ReturnsDraftConcurrency(t *testing.T) {
	cases := []struct {
		name         string
		secondNumber int32
		wantErr      error
	}{
		{name: "same workflow and version number", secondNumber: 1, wantErr: domain.ErrDraftConcurrency},
		{name: "same workflow and a different version number", secondNumber: 2, wantErr: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newTestPool(t)
			ctx := context.Background()
			repo := postgres.NewWorkflowVersionRepo(pool)
			tenantID := uuid.New()
			wf := versionWriteErrorsWorkflow(t, postgres.NewWorkflowRepo(pool), tenantID)
			first := versionWriteErrorsPublished(wf.ID, tenantID, 1)
			if err := repo.Create(ctx, first); err != nil {
				t.Fatalf("create first published version: %v", err)
			}

			err := repo.Create(ctx, versionWriteErrorsPublished(wf.ID, tenantID, tc.secondNumber))

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("second Create = %v, want %v", err, tc.wantErr)
			}
			_, total, listErr := repo.ListByWorkflow(ctx, tenantID, wf.ID, 1, 20)
			if listErr != nil {
				t.Fatalf("ListByWorkflow: %v", listErr)
			}
			wantTotal := int64(1)
			if tc.wantErr == nil {
				wantTotal = 2
			}
			if total != wantTotal {
				t.Errorf("stored versions = %d, want %d", total, wantTotal)
			}
		})
	}
}

func TestWorkflowVersionRepo_Publish_EmptyCompiledPlan_IsRefusedAndTheDraftStaysADraft(t *testing.T) {
	cases := []struct {
		name string
		plan string
	}{
		{name: "empty string", plan: ""},
		{name: "text that is not json", plan: "not json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool := newTestPool(t)
			ctx := context.Background()
			repo := postgres.NewWorkflowVersionRepo(pool)
			tenantID := uuid.New()
			wf := versionWriteErrorsWorkflow(t, postgres.NewWorkflowRepo(pool), tenantID)
			draft := newDraftVersion(wf.ID, tenantID)
			if err := repo.Create(ctx, draft); err != nil {
				t.Fatalf("create draft: %v", err)
			}
			before, err := repo.GetByID(ctx, tenantID, draft.ID)
			if err != nil {
				t.Fatalf("read draft: %v", err)
			}

			err = repo.Publish(ctx, tenantID, draft.ID, before.RecordVersion, 1, tc.plan, "hash", nil)

			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "22P02" {
				t.Fatalf("Publish = %v, want the database's invalid-json error (22P02)", err)
			}
			for _, notWanted := range []error{
				domain.ErrNotFound, domain.ErrVersionNotDraft, domain.ErrDraftConcurrency, domain.ErrUpstreamUnavailable,
			} {
				if errors.Is(err, notWanted) {
					t.Errorf("err = %v, must not be classified as %v", err, notWanted)
				}
			}
			after, err := repo.GetByID(ctx, tenantID, draft.ID)
			if err != nil {
				t.Fatalf("read after refused publish: %v", err)
			}
			if after.Status != domain.VersionStatusDraft || after.VersionNumber != nil ||
				after.RecordVersion != before.RecordVersion {
				t.Errorf("refused publish changed the row: before %+v after %+v", before, after)
			}
		})
	}
}
