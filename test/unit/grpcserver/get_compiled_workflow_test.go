package grpcserver_test

import (
	"context"
	"errors"
	"testing"
	"time"

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
	return grpcadapter.NewServer(testkit.FakeLogger{}, &fakeVersionRepo{getByID: getByID}, nil, time.Hour)
}

func newServerWithCache(
	getByID func(context.Context, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error),
	cache port.CacheStore,
) *grpcadapter.Server {
	return grpcadapter.NewServer(testkit.FakeLogger{}, &fakeVersionRepo{getByID: getByID}, cache, time.Hour)
}

// fakeCache is an in-memory CacheStore for the compiled-plan cache tests.
type fakeCache struct {
	store map[string]string
	sets  int
	gets  int
}

func newFakeCache() *fakeCache { return &fakeCache{store: map[string]string{}} }

func (c *fakeCache) Get(_ context.Context, key string) (string, error) {
	c.gets++
	return c.store[key], nil
}
func (c *fakeCache) Set(_ context.Context, key, value string, _ time.Duration) error {
	c.sets++
	c.store[key] = value
	return nil
}
func (c *fakeCache) Del(_ context.Context, keys ...string) error {
	for _, k := range keys {
		delete(c.store, k)
	}
	return nil
}
func (c *fakeCache) SetNX(context.Context, string, string, time.Duration) (bool, error) {
	return true, nil
}
func (c *fakeCache) Ping(context.Context) error { return nil }

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

func TestGetCompiledWorkflow_EmptyTenantID_PermissionDenied(t *testing.T) {
	srv := newServer(func(_ context.Context, _, _ uuid.UUID) (*domain.WorkflowVersion, error) { return nil, nil })
	_, err := srv.GetCompiledWorkflow(context.Background(), &definitionv1.GetCompiledWorkflowRequest{
		TenantId: "", WorkflowVersionId: gVersionID.String(),
	})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.PermissionDenied {
		t.Errorf("expected codes.PermissionDenied, got %v", err)
	}
}

func TestGetCompiledWorkflow_EmptyVersionID_InvalidArgument(t *testing.T) {
	srv := newServer(func(_ context.Context, _, _ uuid.UUID) (*domain.WorkflowVersion, error) { return nil, nil })
	_, err := srv.GetCompiledWorkflow(context.Background(), &definitionv1.GetCompiledWorkflowRequest{
		TenantId: gTenantID.String(), WorkflowVersionId: "",
	})
	if st, ok := status.FromError(err); !ok || st.Code() != codes.InvalidArgument {
		t.Errorf("expected codes.InvalidArgument, got %v", err)
	}
}

func TestGetCompiledWorkflow_CachePopulateThenHit(t *testing.T) {
	versionNumber := int32(2)
	compiledJSON := `{"name":"cached"}`
	dbCalls := 0
	cache := newFakeCache()
	srv := newServerWithCache(func(_ context.Context, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
		dbCalls++
		return &domain.WorkflowVersion{
			ID: gVersionID, WorkflowID: gWFID, Status: domain.VersionStatusPublished,
			IsValid: true, VersionNumber: &versionNumber, CompiledPlanJSON: &compiledJSON,
		}, nil
	}, cache)

	req := &definitionv1.GetCompiledWorkflowRequest{TenantId: gTenantID.String(), WorkflowVersionId: gVersionID.String()}

	// First call: cache miss → DB read → populate.
	r1, err := srv.GetCompiledWorkflow(context.Background(), req)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	// Second call: served from cache — no further DB read.
	r2, err := srv.GetCompiledWorkflow(context.Background(), req)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if dbCalls != 1 {
		t.Errorf("expected exactly 1 DB read (then cache hit), got %d", dbCalls)
	}
	if cache.sets != 1 {
		t.Errorf("expected 1 cache populate, got %d", cache.sets)
	}
	if r1.CompiledPlanJson != compiledJSON || r2.CompiledPlanJson != compiledJSON {
		t.Errorf("compiled plan mismatch: %q / %q", r1.CompiledPlanJson, r2.CompiledPlanJson)
	}
	if r2.Status != "PUBLISHED" || !r2.IsValid || r2.VersionNumber != 2 {
		t.Errorf("cached response fields wrong: %+v", r2)
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

// errOnGet is a CacheStore that returns an error from Get.
type errOnGet struct{ *fakeCache }

func (e *errOnGet) Get(_ context.Context, _ string) (string, error) {
	return "", errors.New("cache unavailable")
}

func TestGetCompiledWorkflow_CacheGetError_FallsThrough(t *testing.T) {
	// readCache error → fail open → DB read still succeeds.
	versionNumber := int32(1)
	dbCalls := 0
	srv := newServerWithCache(func(_ context.Context, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
		dbCalls++
		return &domain.WorkflowVersion{
			ID: gVersionID, WorkflowID: gWFID, Status: domain.VersionStatusPublished,
			IsValid: true, VersionNumber: &versionNumber,
		}, nil
	}, &errOnGet{newFakeCache()})

	resp, err := srv.GetCompiledWorkflow(context.Background(), &definitionv1.GetCompiledWorkflowRequest{
		TenantId: gTenantID.String(), WorkflowVersionId: gVersionID.String(),
	})
	if err != nil {
		t.Fatalf("cache Get error must not propagate: %v", err)
	}
	if dbCalls != 1 {
		t.Errorf("expected 1 DB call on cache error, got %d", dbCalls)
	}
	if resp.VersionNumber != 1 {
		t.Errorf("want version_number=1, got %d", resp.VersionNumber)
	}
}

func TestGetCompiledWorkflow_CacheInvalidJSON_FallsThrough(t *testing.T) {
	// readCache returns invalid JSON → unmarshal error → fail open → DB read.
	versionNumber := int32(2)
	dbCalls := 0
	cache := newFakeCache()
	cache.store["anything"] = "not-valid-json"

	srv := newServerWithCache(func(_ context.Context, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
		dbCalls++
		return &domain.WorkflowVersion{
			ID: gVersionID, WorkflowID: gWFID, Status: domain.VersionStatusPublished,
			IsValid: true, VersionNumber: &versionNumber,
		}, nil
	}, cache)

	// Pre-populate the exact cache key with bad JSON so Get returns it.
	cacheKey := "wf:plan:" + gTenantID.String() + ":" + gVersionID.String()
	cache.store[cacheKey] = "{invalid"

	resp, err := srv.GetCompiledWorkflow(context.Background(), &definitionv1.GetCompiledWorkflowRequest{
		TenantId: gTenantID.String(), WorkflowVersionId: gVersionID.String(),
	})
	if err != nil {
		t.Fatalf("cache decode error must not propagate: %v", err)
	}
	if dbCalls != 1 {
		t.Errorf("expected 1 DB call on bad JSON, got %d", dbCalls)
	}
	if resp.VersionNumber != 2 {
		t.Errorf("want version_number=2, got %d", resp.VersionNumber)
	}
}

// errOnSet is a CacheStore whose Set always fails.
type errOnSet struct{ *fakeCache }

func (e *errOnSet) Set(_ context.Context, _, _ string, _ time.Duration) error {
	return errors.New("write failure")
}

func TestGetCompiledWorkflow_CacheSetError_ResponseOK(t *testing.T) {
	// writeCache error must not propagate; response is still returned.
	versionNumber := int32(3)
	srv := newServerWithCache(func(_ context.Context, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
		return &domain.WorkflowVersion{
			ID: gVersionID, WorkflowID: gWFID, Status: domain.VersionStatusPublished,
			IsValid: true, VersionNumber: &versionNumber,
		}, nil
	}, &errOnSet{newFakeCache()})

	resp, err := srv.GetCompiledWorkflow(context.Background(), &definitionv1.GetCompiledWorkflowRequest{
		TenantId: gTenantID.String(), WorkflowVersionId: gVersionID.String(),
	})
	if err != nil {
		t.Fatalf("cache Set error must not propagate: %v", err)
	}
	if resp.VersionNumber != 3 {
		t.Errorf("want version_number=3, got %d", resp.VersionNumber)
	}
}
