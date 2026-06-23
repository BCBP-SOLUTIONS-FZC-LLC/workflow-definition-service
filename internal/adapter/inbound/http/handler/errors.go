package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/trace"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type ErrCode string

const (
	CodeNotFound             ErrCode = "NOT_FOUND"
	CodeUnauthorized         ErrCode = "UNAUTHORIZED"
	CodeForbidden            ErrCode = "FORBIDDEN"
	CodeBadRequest           ErrCode = "BAD_REQUEST"
	CodeDraftAlreadyExists   ErrCode = "DRAFT_ALREADY_EXISTS"
	CodeDraftNotFound        ErrCode = "DRAFT_NOT_FOUND"
	CodeNoActiveVersion      ErrCode = "NO_ACTIVE_VERSION"
	CodeDuplicateKey         ErrCode = "DUPLICATE_BUSINESS_KEY"
	CodeDraftConcurrency     ErrCode = "DRAFT_CONCURRENCY"
	CodeInvalidStatus        ErrCode = "INVALID_VERSION_STATUS"
	CodeActiveInstances      ErrCode = "ACTIVE_INSTANCES_EXIST"
	CodeStructuralDiv        ErrCode = "STRUCTURAL_DIVERGENCE"
	CodePlanQuota            ErrCode = "PLAN_QUOTA_EXCEEDED"
	CodeAssigneeIneligible   ErrCode = "ASSIGNEE_INELIGIBLE"
	CodeUpstream             ErrCode = "UPSTREAM_UNAVAILABLE"
	CodeIdempotencyReplay    ErrCode = "IDEMPOTENCY_KEY_REPLAY"
	CodeBPMNValidation       ErrCode = "BPMN_VALIDATION_FAILED"
	CodeInvalidBPMNXML       ErrCode = "INVALID_BPMN_XML"
	CodePayloadTooLarge      ErrCode = "PAYLOAD_TOO_LARGE"
	CodeUnsupportedMediaType ErrCode = "UNSUPPORTED_MEDIA_TYPE"
	CodeInternal             ErrCode = "INTERNAL_ERROR"
)

type InvalidParam struct {
	Name   string  `json:"name"`
	Reason string  `json:"reason"`
	Code   ErrCode `json:"code,omitempty"`
}

// ProblemDetails is the RFC-9457 error envelope returned on all non-2xx responses.
type ProblemDetails struct {
	Type          string         `json:"type"`
	Title         string         `json:"title"`
	Status        int            `json:"status"`
	Detail        string         `json:"detail"`
	Instance      string         `json:"instance"`
	Code          ErrCode        `json:"code"`
	InvalidParams []InvalidParam `json:"invalid_params,omitempty"`
}

const errBase = "https://api.workflow.platform/errors/"

var problemTypes = map[int]string{
	http.StatusBadRequest:            errBase + "bad-request",
	http.StatusUnauthorized:          errBase + "unauthorized",
	http.StatusForbidden:             errBase + "forbidden",
	http.StatusNotFound:              errBase + "not-found",
	http.StatusConflict:              errBase + "conflict",
	http.StatusUnprocessableEntity:   errBase + "validation-failed",
	http.StatusRequestEntityTooLarge: errBase + "payload-too-large",
	http.StatusUnsupportedMediaType:  errBase + "unsupported-media-type",
	http.StatusInternalServerError:   errBase + "internal-error",
	http.StatusServiceUnavailable:    errBase + "service-unavailable",
	http.StatusNotImplemented:        errBase + "not-implemented",
}

var codeTitles = map[ErrCode]string{
	CodeNotFound:             "Resource Not Found",
	CodeUnauthorized:         "Unauthorized",
	CodeForbidden:            "Forbidden",
	CodeBadRequest:           "Bad Request",
	CodeDraftAlreadyExists:   "Draft Already Exists",
	CodeDraftNotFound:        "Draft Not Found",
	CodeNoActiveVersion:      "No Active Version",
	CodeDuplicateKey:         "Duplicate Business Key",
	CodeDraftConcurrency:     "Draft Concurrency Violation",
	CodeInvalidStatus:        "Invalid Version Status",
	CodeActiveInstances:      "Active Instances Exist",
	CodeStructuralDiv:        "Structural Divergence",
	CodePlanQuota:            "Plan Quota Exceeded",
	CodeAssigneeIneligible:   "Assignee Ineligible",
	CodeUpstream:             "Upstream Service Unavailable",
	CodeIdempotencyReplay:    "Idempotency Key Replay",
	CodeBPMNValidation:       "BPMN Validation Failed",
	CodeInvalidBPMNXML:       "Invalid BPMN XML",
	CodePayloadTooLarge:      "Payload Too Large",
	CodeUnsupportedMediaType: "Unsupported Media Type",
	CodeInternal:             "Internal Server Error",
}

func writeProblem(c *gin.Context, status int, code ErrCode, detail string, params []InvalidParam) {
	typeURI, ok := problemTypes[status]
	if !ok {
		typeURI = errBase + "internal-error"
	}
	c.JSON(status, ProblemDetails{
		Type:          typeURI,
		Title:         codeTitles[code],
		Status:        status,
		Detail:        detail,
		Instance:      c.Request.URL.Path,
		Code:          code,
		InvalidParams: params,
	})
}

// bindErrResponse handles errors returned by c.ShouldBindJSON. It returns 413
// when err is *http.MaxBytesError (body exceeded the LimitRequestBody cap), and
// 400 BAD_REQUEST for all other bind failures.
func bindErrResponse(c *gin.Context, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		writeProblem(c, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
			"request body exceeds the 10 MB limit", nil)
		return
	}
	writeProblem(c, http.StatusBadRequest, CodeBadRequest, err.Error(), nil)
}

// errResponse maps a domain or infrastructure error to an RFC-9457 HTTP response
// and logs 5xx outcomes with structured context (tenant, user, trace ID).
// Bind errors (including *http.MaxBytesError from oversized bodies) must be routed
// through bindErrResponse, not here — this function handles service/domain errors only.
// Pass log=nil to suppress 5xx logging (e.g. from middleware without handler context).
func errResponse(c *gin.Context, log port.Logger, err error) {
	status, code, detail, params := mapErr(err)
	if log != nil && status >= 500 {
		tenantID, userID := "unknown", "unknown"
		if rc, ok := gincommon.RequestContext(c); ok {
			tenantID = rc.TenantID
			userID = rc.UserID
		}
		log.Error("request failed", map[string]any{
			"status":    status,
			"code":      string(code),
			"error":     err.Error(),
			"tenant_id": tenantID,
			"user_id":   userID,
			"path":      c.Request.URL.Path,
			"trace_id":  trace.SpanFromContext(c.Request.Context()).SpanContext().TraceID().String(),
		})
	}
	writeProblem(c, status, code, detail, params)
}

// mapErr converts a domain/infrastructure error into its HTTP status, error code,
// detail message, and optional invalid-params list. Extracted so the mapping can be
// tested independently of the Gin context.
func mapErr(err error) (status int, code ErrCode, detail string, params []InvalidParam) {
	var valErr *domain.ValidationFailedError
	switch {
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, pgx.ErrNoRows):
		return http.StatusNotFound, CodeNotFound, err.Error(), nil
	case errors.Is(err, domain.ErrNoDraftExists):
		return http.StatusNotFound, CodeDraftNotFound, err.Error(), nil
	case errors.Is(err, domain.ErrNoActiveVersion):
		return http.StatusConflict, CodeNoActiveVersion, err.Error(), nil
	case errors.Is(err, domain.ErrUnauthorized):
		return http.StatusUnauthorized, CodeUnauthorized, err.Error(), nil
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden, CodeForbidden, err.Error(), nil
	case errors.Is(err, domain.ErrDraftAlreadyExists):
		return http.StatusConflict, CodeDraftAlreadyExists, err.Error(), nil
	case errors.Is(err, domain.ErrDuplicateBusinessKey):
		return http.StatusConflict, CodeDuplicateKey, err.Error(), nil
	case errors.Is(err, domain.ErrDraftConcurrency):
		return http.StatusConflict, CodeDraftConcurrency, err.Error(), nil
	case errors.Is(err, domain.ErrInvalidVersionStatus),
		errors.Is(err, domain.ErrVersionNotDraft),
		errors.Is(err, domain.ErrVersionNotPublished),
		errors.Is(err, domain.ErrVersionAlreadyPublished):
		return http.StatusConflict, CodeInvalidStatus, err.Error(), nil
	case errors.Is(err, domain.ErrActiveInstancesExist):
		return http.StatusConflict, CodeActiveInstances, err.Error(), nil
	case errors.Is(err, domain.ErrStructuralDivergence):
		return http.StatusConflict, CodeStructuralDiv, err.Error(), nil
	case errors.Is(err, domain.ErrIdempotencyKeyReplay):
		return http.StatusConflict, CodeIdempotencyReplay, err.Error(), nil
	case errors.Is(err, domain.ErrPlanQuotaExceeded):
		return http.StatusForbidden, CodePlanQuota, err.Error(), nil
	case errors.Is(err, domain.ErrAssigneeIneligible):
		return http.StatusUnprocessableEntity, CodeAssigneeIneligible, err.Error(), nil
	case errors.As(err, &valErr):
		p := make([]InvalidParam, len(valErr.Errors))
		for i, e := range valErr.Errors {
			p[i] = InvalidParam{Name: e.NodeID, Reason: e.Message, Code: ErrCode(e.Code)}
		}
		return http.StatusUnprocessableEntity, CodeBPMNValidation,
			"BPMN semantic or structural validation failed.", p
	case errors.Is(err, domain.ErrUpstreamUnavailable):
		return http.StatusServiceUnavailable, CodeUpstream, err.Error(), nil
	case errors.Is(err, domain.ErrMalformedBPMN):
		// Covers forbidden-construct (ErrForbiddenXML) failures too — the client
		// response is identical so a probe is not confirmed; the security log is
		// emitted by the handler that accepted the BPMN.
		return http.StatusBadRequest, CodeInvalidBPMNXML, "the BPMN document could not be parsed", nil
	default:
		return http.StatusInternalServerError, CodeInternal, "an unexpected error occurred", nil
	}
}
