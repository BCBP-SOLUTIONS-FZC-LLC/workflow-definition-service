package grpcserver_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	definitionv1 "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/gen/proto/definition/v1"
	grpcadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/grpc"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/unit/testkit"
)

// ensure _ is used to avoid import cycles on port embed
var _ port.WorkflowVersionRepository = (*fakeVersionRepo)(nil)

var (
	gTenantID  = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	gVersionID = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	gWFID      = uuid.MustParse("33333333-3333-3333-3333-333333333333")
)

type fakeVersionRepo struct {
	getByID                        func(ctx context.Context, tenantID, id uuid.UUID) (*domain.WorkflowVersion, error)
	port.WorkflowVersionRepository // embed for unimplemented methods
}

func (f *fakeVersionRepo) GetByID(ctx context.Context, tenantID, id uuid.UUID) (*domain.WorkflowVersion, error) {
	return f.getByID(ctx, tenantID, id)
}

func newServer(getByID func(context.Context, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error)) *grpcadapter.Server {
	return grpcadapter.NewServer(
		testkit.FakeLogger{},
		&fakeVersionRepo{getByID: getByID},
	)
}


// ── tests ─────────────────────────────────────────────────────────────────────

func TestGetCompiledWorkflow_PublishedVersion(t *testing.T) {
	versionNumber := int32(3)
	compiledJSON := `{"name":"test"}`
	srv := newServer(func(_ context.Context, tID, vID uuid.UUID) (*domain.WorkflowVersion, error) {
		return &domain.WorkflowVersion{
			ID:               gVersionID,
			WorkflowID:       gWFID,
			Status:           domain.VersionStatusPublished,
			IsValid:          true,
			VersionNumber:    &versionNumber,
			CompiledPlanJSON: &compiledJSON,
		}, nil
	})

	resp, err := srv.GetCompiledWorkflow(context.Background(), &definitionv1.GetCompiledWorkflowRequest{
		TenantId:          gTenantID.String(),
		WorkflowVersionId: gVersionID.String(),
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.WorkflowId != gWFID.String() {
		t.Errorf("workflow_id: got %q, want %q", resp.WorkflowId, gWFID.String())
	}
	if resp.VersionId != gVersionID.String() {
		t.Errorf("version_id: got %q, want %q", resp.VersionId, gVersionID.String())
	}
	if resp.VersionNumber != 3 {
		t.Errorf("version_number: got %d, want 3", resp.VersionNumber)
	}
	if resp.Status != "PUBLISHED" {
		t.Errorf("status: got %q, want PUBLISHED", resp.Status)
	}
	if !resp.IsValid {
		t.Error("expected is_valid=true")
	}
	if resp.CompiledPlanJson != compiledJSON {
		t.Errorf("compiled_plan_json: got %q, want %q", resp.CompiledPlanJson, compiledJSON)
	}
}

func TestGetCompiledWorkflow_DraftVersion_NilCompiledPlan(t *testing.T) {
	srv := newServer(func(_ context.Context, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
		return &domain.WorkflowVersion{
			ID:               gVersionID,
			WorkflowID:       gWFID,
			Status:           domain.VersionStatusDraft,
			IsValid:          true,
			VersionNumber:    nil,
			CompiledPlanJSON: nil, // must not panic
		}, nil
	})

	resp, err := srv.GetCompiledWorkflow(context.Background(), &definitionv1.GetCompiledWorkflowRequest{
		TenantId:          gTenantID.String(),
		WorkflowVersionId: gVersionID.String(),
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.CompiledPlanJson != "" {
		t.Errorf("expected empty compiled_plan_json, got %q", resp.CompiledPlanJson)
	}
	if resp.VersionNumber != 0 {
		t.Errorf("expected version_number=0 for draft, got %d", resp.VersionNumber)
	}
}

func TestGetCompiledWorkflow_NotFound(t *testing.T) {
	srv := newServer(func(_ context.Context, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
		return nil, domain.ErrNotFound
	})

	_, err := srv.GetCompiledWorkflow(context.Background(), &definitionv1.GetCompiledWorkflowRequest{
		TenantId:          gTenantID.String(),
		WorkflowVersionId: gVersionID.String(),
	})

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.NotFound {
		t.Errorf("expected codes.NotFound, got %v", err)
	}
}

func TestGetCompiledWorkflow_InvalidTenantID(t *testing.T) {
	srv := newServer(func(_ context.Context, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
		return nil, nil
	})

	_, err := srv.GetCompiledWorkflow(context.Background(), &definitionv1.GetCompiledWorkflowRequest{
		TenantId:          "not-a-uuid",
		WorkflowVersionId: gVersionID.String(),
	})

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("expected codes.InvalidArgument, got %v", err)
	}
}

func TestGetCompiledWorkflow_InvalidVersionID(t *testing.T) {
	srv := newServer(func(_ context.Context, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
		return nil, nil
	})

	_, err := srv.GetCompiledWorkflow(context.Background(), &definitionv1.GetCompiledWorkflowRequest{
		TenantId:          gTenantID.String(),
		WorkflowVersionId: "not-a-uuid",
	})

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("expected codes.InvalidArgument, got %v", err)
	}
}

func TestGetCompiledWorkflow_InternalError(t *testing.T) {
	srv := newServer(func(_ context.Context, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
		return nil, errors.New("unexpected db error")
	})

	_, err := srv.GetCompiledWorkflow(context.Background(), &definitionv1.GetCompiledWorkflowRequest{
		TenantId:          gTenantID.String(),
		WorkflowVersionId: gVersionID.String(),
	})

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.Internal {
		t.Errorf("expected codes.Internal, got %v", err)
	}
}
