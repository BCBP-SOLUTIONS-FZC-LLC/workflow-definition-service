package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

var (
	testTenantID = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	testUserID   = uuid.MustParse("22222222-2222-2222-2222-222222222222")
	testWFID     = uuid.MustParse("33333333-3333-3333-3333-333333333333")
	testVerID    = uuid.MustParse("44444444-4444-4444-4444-444444444444")
	testVerID2   = uuid.MustParse("55555555-5555-5555-5555-555555555555")
)

type fakeWorkflowSvc struct {
	list    func(context.Context, uuid.UUID, port.WorkflowFilter) ([]*domain.Workflow, int64, error)
	create  func(context.Context, uuid.UUID, uuid.UUID, string, string, string, string) (*domain.Workflow, *domain.WorkflowVersion, error)
	get     func(context.Context, uuid.UUID, uuid.UUID, int) (*domain.Workflow, []*domain.WorkflowVersion, error)
	archive func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error
}

func (f *fakeWorkflowSvc) List(ctx context.Context, tenantID uuid.UUID, filter port.WorkflowFilter) ([]*domain.Workflow, int64, error) {
	if f.list != nil {
		return f.list(ctx, tenantID, filter)
	}
	return nil, 0, nil
}
func (f *fakeWorkflowSvc) Create(ctx context.Context, tenantID, userID uuid.UUID, businessKey, name, description, bpmnXML, planTier string) (*domain.Workflow, *domain.WorkflowVersion, error) {
	if f.create != nil {
		return f.create(ctx, tenantID, userID, businessKey, name, description, bpmnXML)
	}
	return nil, nil, nil
}
func (f *fakeWorkflowSvc) Get(ctx context.Context, tenantID, id uuid.UUID, versionsLimit int) (*domain.Workflow, []*domain.WorkflowVersion, error) {
	if f.get != nil {
		return f.get(ctx, tenantID, id, versionsLimit)
	}
	return nil, nil, nil
}
func (f *fakeWorkflowSvc) Archive(ctx context.Context, tenantID, userID, id uuid.UUID) error {
	if f.archive != nil {
		return f.archive(ctx, tenantID, userID, id)
	}
	return nil
}

type fakeDraftSvc struct {
	get     func(context.Context, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error)
	init    func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error)
	update  func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, service.UpdateDraftReq) (*domain.WorkflowVersion, error)
	discard func(context.Context, uuid.UUID, uuid.UUID) error
}

func (f *fakeDraftSvc) Get(ctx context.Context, tenantID, workflowID uuid.UUID) (*domain.WorkflowVersion, error) {
	if f.get != nil {
		return f.get(ctx, tenantID, workflowID)
	}
	return nil, nil
}
func (f *fakeDraftSvc) Init(ctx context.Context, tenantID, userID, workflowID uuid.UUID) (*domain.WorkflowVersion, error) {
	if f.init != nil {
		return f.init(ctx, tenantID, userID, workflowID)
	}
	return nil, nil
}
func (f *fakeDraftSvc) Update(ctx context.Context, tenantID, userID, workflowID uuid.UUID, req service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
	if f.update != nil {
		return f.update(ctx, tenantID, userID, workflowID, req)
	}
	return nil, nil
}
func (f *fakeDraftSvc) Discard(ctx context.Context, tenantID, workflowID uuid.UUID) error {
	if f.discard != nil {
		return f.discard(ctx, tenantID, workflowID)
	}
	return nil
}

type fakeVersionSvc struct {
	list    func(context.Context, uuid.UUID, uuid.UUID, int, int) ([]*domain.WorkflowVersion, int64, error)
	get     func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error)
	publish func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, bool) (*domain.WorkflowVersion, error)
	clone   func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, string, service.CloneReq) (*domain.Workflow, *domain.WorkflowVersion, error)
	promote func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error)
	export  func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (string, string, error)
	diff    func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (*service.DiffResult, error)
}

func (f *fakeVersionSvc) List(ctx context.Context, tenantID, workflowID uuid.UUID, page, limit int) ([]*domain.WorkflowVersion, int64, error) {
	if f.list != nil {
		return f.list(ctx, tenantID, workflowID, page, limit)
	}
	return nil, 0, nil
}
func (f *fakeVersionSvc) Get(ctx context.Context, tenantID, workflowID, versionID uuid.UUID) (*domain.WorkflowVersion, error) {
	if f.get != nil {
		return f.get(ctx, tenantID, workflowID, versionID)
	}
	return nil, nil
}
func (f *fakeVersionSvc) Publish(ctx context.Context, tenantID, userID, workflowID, versionID uuid.UUID, skip bool) (*domain.WorkflowVersion, error) {
	if f.publish != nil {
		return f.publish(ctx, tenantID, userID, workflowID, versionID, skip)
	}
	return nil, nil
}
func (f *fakeVersionSvc) Clone(ctx context.Context, tenantID, userID, workflowID, versionID uuid.UUID, planTier string, req service.CloneReq) (*domain.Workflow, *domain.WorkflowVersion, error) {
	if f.clone != nil {
		return f.clone(ctx, tenantID, userID, workflowID, versionID, planTier, req)
	}
	return nil, nil, nil
}
func (f *fakeVersionSvc) Promote(ctx context.Context, tenantID, userID, workflowID, versionID uuid.UUID) (*domain.WorkflowVersion, error) {
	if f.promote != nil {
		return f.promote(ctx, tenantID, userID, workflowID, versionID)
	}
	return &domain.WorkflowVersion{}, nil
}
func (f *fakeVersionSvc) Export(ctx context.Context, tenantID, workflowID, versionID uuid.UUID) (string, string, error) {
	if f.export != nil {
		return f.export(ctx, tenantID, workflowID, versionID)
	}
	return "", "", nil
}
func (f *fakeVersionSvc) Diff(ctx context.Context, tenantID, workflowID, baseVersionID, targetVersionID uuid.UUID) (*service.DiffResult, error) {
	if f.diff != nil {
		return f.diff(ctx, tenantID, workflowID, baseVersionID, targetVersionID)
	}
	return nil, nil
}

type fakeValidationSvc struct {
	validate func(context.Context, string) (bool, []domain.BPMNValidationError, error)
}

func (f *fakeValidationSvc) Validate(ctx context.Context, bpmnXML string) (bool, []domain.BPMNValidationError, error) {
	if f.validate != nil {
		return f.validate(ctx, bpmnXML)
	}
	return true, nil, nil
}

func registerRoutes(r *gin.Engine, h *handler.Handler) {
	wf := r.Group("/api/v1/workflows")
	wf.GET("", h.ListWorkflows)
	wf.POST("", h.CreateWorkflow)
	wf.POST("/validate", h.ValidateBPMN)
	wf.GET("/:id", h.GetWorkflow)
	wf.GET("/:id/versions", h.ListVersions)
	wf.GET("/:id/draft", h.GetDraft)
	wf.POST("/:id/draft", h.InitDraft)
	wf.PUT("/:id/draft", h.UpdateDraft)
	wf.DELETE("/:id/draft", h.DiscardDraft)
	wf.POST("/:id/archive", h.ArchiveWorkflow)
	wf.GET("/:id/versions/:version_id", h.GetVersion)
	wf.POST("/:id/versions/:version_id/publish", h.PublishVersion)
	wf.POST("/:id/versions/:version_id/clone", h.CloneVersion)
	wf.POST("/:id/versions/:version_id/promote", h.PromoteVersion)
	wf.GET("/:id/versions/:version_id/export", h.ExportBPMN)
	wf.GET("/:id/versions/:version_id/diff/:target_version_id", h.GetVersionDiff)
}

func newRouter(h *handler.Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	registerRoutes(r, h)
	return r
}

func newBareRouter(h *handler.Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerRoutes(r, h)
	return r
}

func newHandler(wf *fakeWorkflowSvc, dr *fakeDraftSvc, vs *fakeVersionSvc, val *fakeValidationSvc) *handler.Handler {
	return handler.New(handler.Services{
		Workflows:  wf,
		Drafts:     dr,
		Versions:   vs,
		Validation: val,
	})
}

func jsonBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

func req(method, path string, body any) *http.Request {
	var r io.Reader
	if body != nil {
		r = jsonBody(body)
	}
	httpReq := httptest.NewRequest(method, path, r)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-tenant-id", testTenantID.String())
	httpReq.Header.Set("x-user-id", testUserID.String())
	return httpReq
}

func do(router *gin.Engine, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

// fakeLogger is a port.Logger that records calls for test assertions.
type fakeLogger struct {
	warnCalls []string
}

func (f *fakeLogger) Debug(string, map[string]any) { /* no-op */ }
func (f *fakeLogger) Info(string, map[string]any)  { /* no-op */ }
func (f *fakeLogger) Error(string, map[string]any) { /* no-op */ }
func (f *fakeLogger) Fatal(string, map[string]any) { /* no-op */ }
func (f *fakeLogger) Warn(msg string, _ map[string]any) {
	f.warnCalls = append(f.warnCalls, msg)
}

// errReader is an io.Reader that always returns an error.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func newWorkflow() *domain.Workflow {
	now := time.Now()
	return &domain.Workflow{
		ID:          testWFID,
		TenantID:    testTenantID,
		BusinessKey: "test-wf",
		Name:        "Test Workflow",
		Description: "desc",
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}

func newDraftVersion() *domain.WorkflowVersion {
	now := time.Now()
	return &domain.WorkflowVersion{
		ID:         testVerID,
		WorkflowID: testWFID,
		TenantID:   testTenantID,
		Status:     domain.VersionStatusDraft,
		BPMNXML:    "<definitions/>",
		IsValid:    true,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func newPublishedVersion() *domain.WorkflowVersion {
	now := time.Now()
	n := int32(1)
	return &domain.WorkflowVersion{
		ID:            testVerID,
		WorkflowID:    testWFID,
		TenantID:      testTenantID,
		Status:        domain.VersionStatusPublished,
		BPMNXML:       "<definitions/>",
		IsValid:       true,
		VersionNumber: &n,
		ArtifactHash:  "abc123",
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}
