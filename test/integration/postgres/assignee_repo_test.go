package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func TestAssigneeRepo_BulkInsert(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	aRepo := postgres.NewAssigneeRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), TemplateKey: "assignee-bulk-insert", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("Create workflow: %v", err)
	}
	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("Create version: %v", err)
	}

	userID := uuid.New()
	assignees := []*domain.NodeAssignee{
		{ID: uuid.New(), NodeKey: "Task_1", UserID: userID, Role: "approver"},
		{ID: uuid.New(), NodeKey: "Task_2", UserID: userID, Role: "reviewer"},
	}

	if err := aRepo.BulkInsert(ctx, tenantID, v.ID, assignees); err != nil {
		t.Fatalf("BulkInsert: %v", err)
	}

	got, err := aRepo.ListByUser(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len(got) = %d, want 2", len(got))
	}
}

func TestAssigneeRepo_BulkInsert_Empty(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	aRepo := postgres.NewAssigneeRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), TemplateKey: "assignee-bulk-empty", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("Create workflow: %v", err)
	}
	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("Create version: %v", err)
	}

	if err := aRepo.BulkInsert(ctx, tenantID, v.ID, nil); err != nil {
		t.Errorf("BulkInsert(nil): %v, want nil", err)
	}
	if err := aRepo.BulkInsert(ctx, tenantID, v.ID, []*domain.NodeAssignee{}); err != nil {
		t.Errorf("BulkInsert(empty): %v, want nil", err)
	}

	got, err := aRepo.ListByUser(ctx, tenantID, uuid.New())
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0", len(got))
	}
}

func TestAssigneeRepo_ListByUser(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	aRepo := postgres.NewAssigneeRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	userID := uuid.New()
	otherUserID := uuid.New()

	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), TemplateKey: "assignee-list-user", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("Create workflow: %v", err)
	}
	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("Create version: %v", err)
	}

	all := []*domain.NodeAssignee{
		{ID: uuid.New(), NodeKey: "Task_A", UserID: userID, Role: "approver"},
		{ID: uuid.New(), NodeKey: "Task_B", UserID: otherUserID, Role: "reviewer"},
	}
	if err := aRepo.BulkInsert(ctx, tenantID, v.ID, all); err != nil {
		t.Fatalf("BulkInsert: %v", err)
	}

	got, err := aRepo.ListByUser(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(got) = %d, want 1", len(got))
	}
	if got[0].NodeKey != "Task_A" {
		t.Errorf("NodeKey = %q, want Task_A", got[0].NodeKey)
	}
	if got[0].WorkflowVersionID != v.ID {
		t.Errorf("WorkflowVersionID = %v, want %v", got[0].WorkflowVersionID, v.ID)
	}

	none, err := aRepo.ListByUser(ctx, tenantID, uuid.New())
	if err != nil {
		t.Fatalf("ListByUser unknown user: %v", err)
	}
	if len(none) != 0 {
		t.Errorf("unknown user: len(got) = %d, want 0", len(none))
	}
}

func TestAssigneeRepo_DeleteByVersion(t *testing.T) {
	pool := newTestPool(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	aRepo := postgres.NewAssigneeRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	userID := uuid.New()

	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), TemplateKey: "assignee-delete-version", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("Create workflow: %v", err)
	}
	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("Create version: %v", err)
	}

	assignees := []*domain.NodeAssignee{
		{ID: uuid.New(), NodeKey: "Task_1", UserID: userID, Role: "approver"},
	}
	if err := aRepo.BulkInsert(ctx, tenantID, v.ID, assignees); err != nil {
		t.Fatalf("BulkInsert: %v", err)
	}

	if err := aRepo.DeleteByVersion(ctx, tenantID, v.ID); err != nil {
		t.Fatalf("DeleteByVersion: %v", err)
	}

	got, err := aRepo.ListByUser(ctx, tenantID, userID)
	if err != nil {
		t.Fatalf("ListByUser after delete: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(got) = %d, want 0 after DeleteByVersion", len(got))
	}
}

func TestAssigneeRepo_CreatedAtIsTheInstantOfInsertWhateverTheSessionTimeZone(t *testing.T) {
	pool, dsn := fixtures.NewTestPoolAndDSN(t)
	wfRepo := postgres.NewWorkflowRepo(pool)
	vRepo := postgres.NewWorkflowVersionRepo(pool)
	aRepo := postgres.NewAssigneeRepo(pool)
	ctx := context.Background()

	tenantID := uuid.New()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID,
		CreatedByUserID: uuid.New(), TemplateKey: "assignee-tz", Name: "X",
	}
	if err := wfRepo.Create(ctx, wf); err != nil {
		t.Fatalf("Create workflow: %v", err)
	}
	v := newDraftVersion(wf.ID, tenantID)
	if err := vRepo.Create(ctx, v); err != nil {
		t.Fatalf("Create version: %v", err)
	}

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, "SET TIME ZONE 'Asia/Dubai'"); err != nil {
		t.Fatalf("set time zone: %v", err)
	}
	userID := uuid.New()
	before := time.Now()
	if _, err := conn.Exec(ctx,
		`INSERT INTO workflow_node_assignee (id, tenant_id, workflow_version_id, node_key, user_id, department_id, role)
		 VALUES ($1, $2, $3, 'Task_1', $4, $5, 'approver')`,
		uuid.New(), tenantID, v.ID, userID, uuid.New()); err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := aRepo.ListByUser(ctx, tenantID, userID)
	if err != nil || len(got) != 1 {
		t.Fatalf("ListByUser = %v, %v", got, err)
	}
	if skew := got[0].CreatedAt.Sub(before); skew < -time.Minute || skew > time.Minute {
		t.Errorf("created_at %s is %s from the insert: a column without a time zone reads back as the session's wall clock, which the revocation time filter compares with a UTC instant", got[0].CreatedAt, skew)
	}
}
