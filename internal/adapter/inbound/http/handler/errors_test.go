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
		{"wrapped sentinel", fmt.Errorf("service layer: %w", domain.ErrNotFound), http.StatusNotFound, CodeNotFound},
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

func TestErrResponse_ValidationFailedError(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		wantParamCount  int
		wantFirstNodeID string
		wantFirstCode   ErrCode
	}{
		{
			name: "direct",
			err: &domain.ValidationFailedError{
				Errors: []domain.BPMNValidationError{
					{Code: domain.BPMNErrCycleDetected, NodeID: "Node_1", Message: "cycle detected"},
					{Code: domain.BPMNErrDanglingNode, NodeID: "Node_2", Message: "dangling node"},
				},
			},
			wantParamCount:  2,
			wantFirstNodeID: "Node_1",
			wantFirstCode:   ErrCode(domain.BPMNErrCycleDetected),
		},
		{
			name: "wrapped",
			err: fmt.Errorf("publish: %w", &domain.ValidationFailedError{
				Errors: []domain.BPMNValidationError{
					{Code: domain.BPMNErrNoStartEvent, Message: "no start event"},
				},
			}),
			wantParamCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := newTestCtx("/validate")
			errResponse(c, nil, tt.err)

			if w.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want 422", w.Code)
			}
			p := decodeProblem(t, w.Body.Bytes())
			if p.Code != CodeBPMNValidation {
				t.Errorf("code = %q, want %q", p.Code, CodeBPMNValidation)
			}
			if len(p.InvalidParams) != tt.wantParamCount {
				t.Fatalf("invalid_params len = %d, want %d", len(p.InvalidParams), tt.wantParamCount)
			}
			if tt.wantFirstNodeID != "" && p.InvalidParams[0].Name != tt.wantFirstNodeID {
				t.Errorf("invalid_params[0].name = %q, want %q", p.InvalidParams[0].Name, tt.wantFirstNodeID)
			}
			if tt.wantFirstCode != "" && p.InvalidParams[0].Code != tt.wantFirstCode {
				t.Errorf("invalid_params[0].code = %q, want %q", p.InvalidParams[0].Code, tt.wantFirstCode)
			}
		})
	}
}

func TestWriteProblem(t *testing.T) {
	tests := []struct {
		name            string
		status          int
		code            ErrCode
		detail          string
		wantContentType string
		wantType        string
	}{
		{
			name:            "sets JSON content type",
			status:          http.StatusNotFound,
			code:            CodeNotFound,
			detail:          "test detail",
			wantContentType: "application/json; charset=utf-8",
		},
		{
			name:     "unknown status falls back to internal-error type",
			status:   http.StatusTeapot,
			code:     CodeInternal,
			detail:   "teapot",
			wantType: errBase + "internal-error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := newTestCtx("/test")
			writeProblem(c, tt.status, tt.code, tt.detail, nil)
			if tt.wantContentType != "" {
				if ct := w.Header().Get("Content-Type"); ct != tt.wantContentType {
					t.Errorf("Content-Type = %q, want %q", ct, tt.wantContentType)
				}
			}
			if tt.wantType != "" {
				p := decodeProblem(t, w.Body.Bytes())
				if p.Type != tt.wantType {
					t.Errorf("type = %q, want %q", p.Type, tt.wantType)
				}
			}
		})
	}
}

func TestErrResponse_Logger(t *testing.T) {
	tests := []struct {
		name       string
		run        func(log *spyLogger) *httptest.ResponseRecorder
		wantStatus int
		wantErrors int
	}{
		{
			name: "5xx logs error",
			run: func(log *spyLogger) *httptest.ResponseRecorder {
				c, w := newTestCtx("/api/v1/something")
				errResponse(c, log, errors.New("unexpected failure"))
				return w
			},
			wantStatus: http.StatusInternalServerError,
			wantErrors: 1,
		},
		{
			name: "4xx does not log",
			run: func(log *spyLogger) *httptest.ResponseRecorder {
				c, w := newTestCtx("/api/v1/something")
				errResponse(c, log, domain.ErrNotFound)
				return w
			},
			wantStatus: http.StatusNotFound,
			wantErrors: 0,
		},
		{
			name: "5xx with request context",
			run: func(log *spyLogger) *httptest.ResponseRecorder {
				r := gin.New()
				for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
					r.Use(mw)
				}
				r.GET("/test", func(c *gin.Context) {
					errResponse(c, log, errors.New("something internal"))
				})
				w := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/test", nil)
				req.Header.Set("x-tenant-id", "11111111-1111-1111-1111-111111111111")
				req.Header.Set("x-user-id", "22222222-2222-2222-2222-222222222222")
				r.ServeHTTP(w, req)
				return w
			},
			wantStatus: http.StatusInternalServerError,
			wantErrors: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := &spyLogger{}
			w := tt.run(log)
			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if log.errors != tt.wantErrors {
				t.Errorf("log.errors = %d, want %d", log.errors, tt.wantErrors)
			}
		})
	}
}

func TestLogForbiddenXML(t *testing.T) {
	tests := []struct {
		name      string
		nilLogger bool
		err       error
		useRC     bool
		wantWarns int
	}{
		{name: "warns on forbidden XML", err: domain.ErrForbiddenXML, wantWarns: 1},
		{name: "skips nil logger", nilLogger: true, err: domain.ErrForbiddenXML},
		{name: "skips non-forbidden error", err: errors.New("some other parse error")},
		{name: "warns with request context", err: domain.ErrForbiddenXML, useRC: true, wantWarns: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			log := &spyLogger{}
			var svc Services
			if !tt.nilLogger {
				svc.Log = log
			}
			h := New(svc)

			if tt.useRC {
				r := gin.New()
				for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
					r.Use(mw)
				}
				r.GET("/upload", func(c *gin.Context) { h.logForbiddenXML(c, tt.err) })
				w := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/upload", nil)
				req.Header.Set("x-tenant-id", "11111111-1111-1111-1111-111111111111")
				req.Header.Set("x-user-id", "22222222-2222-2222-2222-222222222222")
				r.ServeHTTP(w, req)
			} else {
				c, _ := newTestCtx("/api/v1/upload")
				h.logForbiddenXML(c, tt.err)
			}

			if log.warns != tt.wantWarns {
				t.Errorf("warns = %d, want %d", log.warns, tt.wantWarns)
			}
		})
	}
}
