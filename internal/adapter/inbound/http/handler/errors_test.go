package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type spyLogger struct {
	errors int
	warns  int
}

func (l *spyLogger) Info(string, map[string]any)      {}
func (l *spyLogger) Error(_ string, _ map[string]any) { l.errors++ }
func (l *spyLogger) Fatal(string, map[string]any)     {}
func (l *spyLogger) Warn(_ string, _ map[string]any)  { l.warns++ }
func (l *spyLogger) Debug(string, map[string]any)     {}

var _ port.Logger = (*spyLogger)(nil)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestCtx(path string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, path, nil)
	return c, w
}

func decodeProblem(t *testing.T, body []byte) ProblemDetails {
	t.Helper()
	var p ProblemDetails
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("failed to decode ProblemDetails: %v\nbody: %s", err, body)
	}
	return p
}

func TestErrResponse_StatusAndCode(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   ErrCode
	}{
		// 404
		{"ErrNotFound", domain.ErrNotFound, http.StatusNotFound, CodeNotFound},
		{"pgx.ErrNoRows", pgx.ErrNoRows, http.StatusNotFound, CodeNotFound},
		{"ErrNoDraftExists", domain.ErrNoDraftExists, http.StatusNotFound, CodeDraftNotFound},
		// 401 / 403
		{"ErrUnauthorized", domain.ErrUnauthorized, http.StatusUnauthorized, CodeUnauthorized},
		{"ErrForbidden", domain.ErrForbidden, http.StatusForbidden, CodeForbidden},
		{"ErrPlanQuotaExceeded", domain.ErrPlanQuotaExceeded, http.StatusForbidden, CodePlanQuota},
		// 409
		{"ErrNoActiveVersion", domain.ErrNoActiveVersion, http.StatusConflict, CodeNoActiveVersion},
		{"ErrDraftAlreadyExists", domain.ErrDraftAlreadyExists, http.StatusConflict, CodeDraftAlreadyExists},
		{"ErrDuplicateBusinessKey", domain.ErrDuplicateBusinessKey, http.StatusConflict, CodeDuplicateKey},
		{"ErrDraftConcurrency", domain.ErrDraftConcurrency, http.StatusConflict, CodeDraftConcurrency},
		{"ErrInvalidVersionStatus", domain.ErrInvalidVersionStatus, http.StatusConflict, CodeInvalidStatus},
		{"ErrVersionNotDraft", domain.ErrVersionNotDraft, http.StatusConflict, CodeInvalidStatus},
		{"ErrVersionNotPublished", domain.ErrVersionNotPublished, http.StatusConflict, CodeInvalidStatus},
		{"ErrVersionAlreadyPublished", domain.ErrVersionAlreadyPublished, http.StatusConflict, CodeInvalidStatus},
		{"ErrActiveInstancesExist", domain.ErrActiveInstancesExist, http.StatusConflict, CodeActiveInstances},
		{"ErrStructuralDivergence", domain.ErrStructuralDivergence, http.StatusConflict, CodeStructuralDiv},
		{"ErrIdempotencyKeyReplay", domain.ErrIdempotencyKeyReplay, http.StatusConflict, CodeIdempotencyReplay},
		// 422
		{"ErrAssigneeIneligible", domain.ErrAssigneeIneligible, http.StatusUnprocessableEntity, CodeAssigneeIneligible},
		// 503
		{"ErrUpstreamUnavailable", domain.ErrUpstreamUnavailable, http.StatusServiceUnavailable, CodeUpstream},
		// 500 fallback
		{"unknown error", errors.New("something unexpected"), http.StatusInternalServerError, CodeInternal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := newTestCtx("/test-path")
			errResponse(c, nil, tt.err)

			if w.Code != tt.wantStatus {
				t.Errorf("HTTP status = %d, want %d", w.Code, tt.wantStatus)
			}

			p := decodeProblem(t, w.Body.Bytes())
			if p.Code != tt.wantCode {
				t.Errorf("code = %q, want %q", p.Code, tt.wantCode)
			}
			if p.Status != tt.wantStatus {
				t.Errorf("body.status = %d, want %d", p.Status, tt.wantStatus)
			}
			if p.Instance != "/test-path" {
				t.Errorf("instance = %q, want %q", p.Instance, "/test-path")
			}
			if p.Type == "" {
				t.Error("type must not be empty")
			}
			if p.Title == "" {
				t.Error("title must not be empty")
			}
		})
	}
}

func TestErrResponse_WrappedSentinel(t *testing.T) {
	wrapped := fmt.Errorf("service layer: %w", domain.ErrNotFound)
	c, w := newTestCtx("/wrap")
	errResponse(c, nil, wrapped)

	if w.Code != http.StatusNotFound {
		t.Errorf("wrapped sentinel: status = %d, want 404", w.Code)
	}
	p := decodeProblem(t, w.Body.Bytes())
	if p.Code != CodeNotFound {
		t.Errorf("wrapped sentinel: code = %q, want %q", p.Code, CodeNotFound)
	}
}

func TestErrResponse_ValidationFailedError(t *testing.T) {
	valErr := &domain.ValidationFailedError{
		Errors: []domain.BPMNValidationError{
			{Code: domain.BPMNErrCycleDetected, NodeID: "Node_1", Message: "cycle detected"},
			{Code: domain.BPMNErrDanglingNode, NodeID: "Node_2", Message: "dangling node"},
		},
	}
	c, w := newTestCtx("/publish")
	errResponse(c, nil, valErr)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", w.Code)
	}

	p := decodeProblem(t, w.Body.Bytes())
	if p.Code != CodeBPMNValidation {
		t.Errorf("code = %q, want %q", p.Code, CodeBPMNValidation)
	}
	if len(p.InvalidParams) != 2 {
		t.Fatalf("invalid_params len = %d, want 2", len(p.InvalidParams))
	}
	if p.InvalidParams[0].Name != "Node_1" {
		t.Errorf("invalid_params[0].name = %q, want %q", p.InvalidParams[0].Name, "Node_1")
	}
	if p.InvalidParams[0].Code != ErrCode(domain.BPMNErrCycleDetected) {
		t.Errorf("invalid_params[0].code = %q, want %q", p.InvalidParams[0].Code, domain.BPMNErrCycleDetected)
	}
}

func TestErrResponse_ValidationFailedError_Wrapped(t *testing.T) {
	valErr := &domain.ValidationFailedError{
		Errors: []domain.BPMNValidationError{
			{Code: domain.BPMNErrNoStartEvent, Message: "no start event"},
		},
	}
	wrapped := fmt.Errorf("publish: %w", valErr)
	c, w := newTestCtx("/wrapped-val")
	errResponse(c, nil, wrapped)

	if w.Code != http.StatusUnprocessableEntity {
		t.Errorf("wrapped ValidationFailedError: status = %d, want 422", w.Code)
	}
}

func TestWriteProblem_ContentType(t *testing.T) {
	c, w := newTestCtx("/ct")
	writeProblem(c, http.StatusNotFound, CodeNotFound, "test detail", nil)

	ct := w.Header().Get("Content-Type")
	if ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q, want application/json; charset=utf-8", ct)
	}
}

func TestWriteProblem_UnknownStatus_FallsBackToInternalError(t *testing.T) {
	c, w := newTestCtx("/unknown")
	writeProblem(c, http.StatusTeapot, CodeInternal, "teapot", nil)

	p := decodeProblem(t, w.Body.Bytes())
	if p.Type != errBase+"internal-error" {
		t.Errorf("unexpected type for unmapped status: %q", p.Type)
	}
}

func TestErrResponse_5xxWithLogger(t *testing.T) {
	log := &spyLogger{}
	c, w := newTestCtx("/api/v1/something")
	errResponse(c, log, errors.New("unexpected failure"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d", w.Code)
	}
	if log.errors != 1 {
		t.Errorf("want 1 log.Error call, got %d", log.errors)
	}
}

func TestErrResponse_4xxWithLogger_NoLog(t *testing.T) {
	log := &spyLogger{}
	c, w := newTestCtx("/api/v1/something")
	errResponse(c, log, domain.ErrNotFound)

	if w.Code != http.StatusNotFound {
		t.Errorf("want 404, got %d", w.Code)
	}
	if log.errors != 0 {
		t.Errorf("4xx should not log error, got %d log.Error calls", log.errors)
	}
}

func TestLogForbiddenXML_LogsWarnForForbiddenXML(t *testing.T) {
	log := &spyLogger{}
	h := New(Services{Log: log})
	c, _ := newTestCtx("/api/v1/upload")
	h.logForbiddenXML(c, domain.ErrForbiddenXML)

	if log.warns != 1 {
		t.Errorf("want 1 log.Warn call for ErrForbiddenXML, got %d", log.warns)
	}
}

func TestLogForbiddenXML_SkipsNilLogger(t *testing.T) {
	h := New(Services{})
	c, _ := newTestCtx("/api/v1/upload")
	h.logForbiddenXML(c, domain.ErrForbiddenXML) // must not panic
}

func TestLogForbiddenXML_SkipsNonForbiddenError(t *testing.T) {
	log := &spyLogger{}
	h := New(Services{Log: log})
	c, _ := newTestCtx("/api/v1/upload")
	h.logForbiddenXML(c, errors.New("some other parse error"))

	if log.warns != 0 {
		t.Errorf("non-forbidden error must not emit warn, got %d", log.warns)
	}
}

func TestErrResponse_5xxWithRequestContext(t *testing.T) {
	log := &spyLogger{}
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.GET("/test", func(c *gin.Context) {
		errResponse(c, log, errors.New("something internal"))
	})

	w := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodGet, "/test", nil)
	httpReq.Header.Set("x-tenant-id", "11111111-1111-1111-1111-111111111111")
	httpReq.Header.Set("x-user-id", "22222222-2222-2222-2222-222222222222")
	r.ServeHTTP(w, httpReq)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("want 500, got %d", w.Code)
	}
	if log.errors != 1 {
		t.Errorf("want 1 Error call with request context, got %d", log.errors)
	}
}

func TestLogForbiddenXML_WithRequestContext(t *testing.T) {
	log := &spyLogger{}
	h := New(Services{Log: log})
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.GET("/upload", func(c *gin.Context) {
		h.logForbiddenXML(c, domain.ErrForbiddenXML)
	})

	w := httptest.NewRecorder()
	httpReq := httptest.NewRequest(http.MethodGet, "/upload", nil)
	httpReq.Header.Set("x-tenant-id", "11111111-1111-1111-1111-111111111111")
	httpReq.Header.Set("x-user-id", "22222222-2222-2222-2222-222222222222")
	r.ServeHTTP(w, httpReq)

	if log.warns != 1 {
		t.Errorf("want 1 Warn with tenant context, got %d", log.warns)
	}
}
