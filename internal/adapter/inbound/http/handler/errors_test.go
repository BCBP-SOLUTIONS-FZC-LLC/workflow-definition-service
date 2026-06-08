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

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

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
		{"ErrNoActiveVersion", domain.ErrNoActiveVersion, http.StatusNotFound, CodeNoActiveVersion},
		// 401 / 403
		{"ErrUnauthorized", domain.ErrUnauthorized, http.StatusUnauthorized, CodeUnauthorized},
		{"ErrForbidden", domain.ErrForbidden, http.StatusForbidden, CodeForbidden},
		// 409
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
		{"ErrPlanQuotaExceeded", domain.ErrPlanQuotaExceeded, http.StatusUnprocessableEntity, CodePlanQuota},
		{"ErrAssigneeIneligible", domain.ErrAssigneeIneligible, http.StatusUnprocessableEntity, CodeAssigneeIneligible},
		// 503
		{"ErrUpstreamUnavailable", domain.ErrUpstreamUnavailable, http.StatusServiceUnavailable, CodeUpstream},
		// 500 fallback
		{"unknown error", errors.New("something unexpected"), http.StatusInternalServerError, CodeInternal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := newTestCtx("/test-path")
			errResponse(c, tt.err)

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
	errResponse(c, wrapped)

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
	errResponse(c, valErr)

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
	errResponse(c, wrapped)

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
