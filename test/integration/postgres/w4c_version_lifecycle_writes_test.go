//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestWorkflowVersionRepo_Archive_StatusDecidesTheOutcome(t *testing.T) {
	cases := []struct {
		name       string
		status     domain.VersionStatus
		archives   int
		wantErrs   []error
		wantStatus domain.VersionStatus
	}{
		{"published version is archived", domain.VersionStatusPublished, 1, []error{nil}, domain.VersionStatusArchived},
		{"published version archived twice refuses the repeat", domain.VersionStatusPublished, 2, []error{nil, domain.ErrVersionNotPublished}, domain.VersionStatusArchived},
		{"draft version is refused", domain.VersionStatusDraft, 1, []error{domain.ErrVersionNotPublished}, domain.VersionStatusDraft},
		{"archived version is refused", domain.VersionStatusArchived, 1, []error{domain.ErrVersionNotPublished}, domain.VersionStatusArchived},
	}
	pool := newTestPool(t)
	repo := postgres.NewWorkflowVersionRepo(pool)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wf := w4cWorkflow(t, pool, uuid.New())
			stored := w4cVersionInStatus(t, pool, wf, tc.status, 1)

			for i, want := range tc.wantErrs {
				err := repo.Archive(context.Background(), wf.TenantID, stored.ID)
				if want == nil && err != nil {
					t.Fatalf("Archive call %d = %v, want success", i+1, err)
				}
				if want != nil && !errors.Is(err, want) {
					t.Fatalf("Archive call %d = %v, want %v", i+1, err, want)
				}
			}

			got := w4cStored(t, pool, wf.TenantID, stored.ID)
			if got.Status != tc.wantStatus {
				t.Errorf("status = %s, want %s", got.Status, tc.wantStatus)
			}
			if w4cSnapshot(t, w4cWithoutStatusAndStamps(got)) != w4cSnapshot(t, w4cWithoutStatusAndStamps(stored)) {
				t.Errorf("archiving changed more than the status:\n before %+v\n after  %+v", stored, got)
			}
		})
	}
}

func w4cWithoutStatusAndStamps(v *domain.WorkflowVersion) *domain.WorkflowVersion {
	c := *v
	c.Status = ""
	c.UpdatedAt = time.Time{}
	c.RecordVersion = 0
	return &c
}

func TestWorkflowVersionRepo_DeleteDraft_StatusDecidesTheOutcome(t *testing.T) {
	cases := []struct {
		name     string
		status   domain.VersionStatus
		wantErr  error
		wantKept bool
	}{
		{"draft version is deleted", domain.VersionStatusDraft, nil, false},
		{"published version is refused and kept", domain.VersionStatusPublished, domain.ErrVersionNotDraft, true},
		{"archived version is refused and kept", domain.VersionStatusArchived, domain.ErrVersionNotDraft, true},
	}
	pool := newTestPool(t)
	repo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wf := w4cWorkflow(t, pool, uuid.New())
			sibling := w4cVersionInStatus(t, pool, wf, domain.VersionStatusPublished, 1)
			target := sibling
			if tc.status != domain.VersionStatusPublished {
				number := int32(2)
				target = w4cVersionInStatus(t, pool, wf, tc.status, number)
			}
			before := w4cSnapshot(t, target)

			err := repo.DeleteDraft(ctx, wf.TenantID, target.ID)

			if tc.wantErr == nil && err != nil {
				t.Fatalf("DeleteDraft = %v, want success", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("DeleteDraft = %v, want %v", err, tc.wantErr)
			}
			got, getErr := repo.GetByID(ctx, wf.TenantID, target.ID)
			if tc.wantKept {
				if getErr != nil || w4cSnapshot(t, got) != before {
					t.Fatalf("refused delete must keep the row unchanged: err %v, row %+v, want %s", getErr, got, before)
				}
			} else if !errors.Is(getErr, domain.ErrNotFound) {
				t.Fatalf("after deleting the draft GetByID = %v, want ErrNotFound", getErr)
			}
			if tc.status == domain.VersionStatusDraft {
				if kept := w4cStored(t, pool, wf.TenantID, sibling.ID); kept.Status != domain.VersionStatusPublished {
					t.Errorf("the published sibling is %s after the draft was deleted, want PUBLISHED", kept.Status)
				}
			}
		})
	}
}

func TestWorkflowVersionRepo_SetInvalid_AnyStatus_MarksInvalidAndMergesTheErrorList(t *testing.T) {
	statuses := []domain.VersionStatus{
		domain.VersionStatusDraft, domain.VersionStatusPublished, domain.VersionStatusArchived,
	}
	pool := newTestPool(t)
	repo := postgres.NewWorkflowVersionRepo(pool)
	ctx := context.Background()
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			wf := w4cWorkflow(t, pool, uuid.New())
			stored := w4cVersionInStatus(t, pool, wf, status, 1)
			if !stored.IsValid || stored.ValidationErrorsJSON != nil {
				t.Fatalf("precondition: stored valid=%v errors=%v, want a valid version without errors", stored.IsValid, stored.ValidationErrorsJSON)
			}

			steps := []struct {
				input string
				want  string
			}{
				{`[{"node_id":"A","error":"gone"}]`, `[{"node_id":"A","error":"gone"}]`},
				{`[{"node_id":"B","error":"gone"}]`, `[{"node_id":"A","error":"gone"},{"node_id":"B","error":"gone"}]`},
			}
			for i, step := range steps {
				if err := repo.SetInvalid(ctx, wf.TenantID, stored.ID, step.input); err != nil {
					t.Fatalf("SetInvalid %d on a %s version: %v", i+1, status, err)
				}
				got := w4cStored(t, pool, wf.TenantID, stored.ID)
				if got.IsValid {
					t.Errorf("after SetInvalid %d is_valid = true, want false", i+1)
				}
				if got.ValidationErrorsJSON == nil || !w4cJSONEqual(*got.ValidationErrorsJSON, step.want) {
					t.Errorf("after SetInvalid %d errors = %v, want %s", i+1, got.ValidationErrorsJSON, step.want)
				}
				if got.Status != status || got.BPMNXML != stored.BPMNXML || got.ArtifactHash != stored.ArtifactHash {
					t.Errorf("after SetInvalid %d status %s hash %q bpmn %q changed from %s %q %q",
						i+1, got.Status, got.ArtifactHash, got.BPMNXML, status, stored.ArtifactHash, stored.BPMNXML)
				}
			}
		})
	}
}
