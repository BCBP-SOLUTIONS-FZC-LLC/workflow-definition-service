package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type ErrCode string

const (
	CodeNotFound           ErrCode = "NOT_FOUND"
	CodeUnauthorized       ErrCode = "UNAUTHORIZED"
	CodeForbidden          ErrCode = "FORBIDDEN"
	CodeBadRequest         ErrCode = "BAD_REQUEST"
	CodeDraftAlreadyExists ErrCode = "DRAFT_ALREADY_EXISTS"
	CodeDraftNotFound      ErrCode = "DRAFT_NOT_FOUND"
	CodeNoActiveVersion    ErrCode = "NO_ACTIVE_VERSION"
	CodeDuplicateKey       ErrCode = "DUPLICATE_BUSINESS_KEY"
	CodeDraftConcurrency   ErrCode = "DRAFT_CONCURRENCY"
	CodeInvalidStatus      ErrCode = "INVALID_VERSION_STATUS"
	CodeActiveInstances    ErrCode = "ACTIVE_INSTANCES_EXIST"
	CodeStructuralDiv      ErrCode = "STRUCTURAL_DIVERGENCE"
	CodePlanQuota          ErrCode = "PLAN_QUOTA_EXCEEDED"
	CodeAssigneeIneligible ErrCode = "ASSIGNEE_INELIGIBLE"
	CodeUpstream           ErrCode = "UPSTREAM_UNAVAILABLE"
	CodeIdempotencyReplay  ErrCode = "IDEMPOTENCY_KEY_REPLAY"
	CodeBPMNValidation     ErrCode = "BPMN_VALIDATION_FAILED"
	CodeInternal           ErrCode = "INTERNAL_ERROR"
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
	http.StatusBadRequest:          errBase + "bad-request",
	http.StatusUnauthorized:        errBase + "unauthorized",
	http.StatusForbidden:           errBase + "forbidden",
	http.StatusNotFound:            errBase + "not-found",
	http.StatusConflict:            errBase + "conflict",
	http.StatusUnprocessableEntity: errBase + "validation-failed",
	http.StatusInternalServerError: errBase + "internal-error",
	http.StatusServiceUnavailable:  errBase + "service-unavailable",
	http.StatusNotImplemented:      errBase + "not-implemented",
}

var codeTitles = map[ErrCode]string{
	CodeNotFound:           "Resource Not Found",
	CodeUnauthorized:       "Unauthorized",
	CodeForbidden:          "Forbidden",
	CodeBadRequest:         "Bad Request",
	CodeDraftAlreadyExists: "Draft Already Exists",
	CodeDraftNotFound:      "Draft Not Found",
	CodeNoActiveVersion:    "No Active Version",
	CodeDuplicateKey:       "Duplicate Business Key",
	CodeDraftConcurrency:   "Draft Concurrency Violation",
	CodeInvalidStatus:      "Invalid Version Status",
	CodeActiveInstances:    "Active Instances Exist",
	CodeStructuralDiv:      "Structural Divergence",
	CodePlanQuota:          "Plan Quota Exceeded",
	CodeAssigneeIneligible: "Assignee Ineligible",
	CodeUpstream:           "Upstream Service Unavailable",
	CodeIdempotencyReplay:  "Idempotency Key Replay",
	CodeBPMNValidation:     "BPMN Validation Failed",
	CodeInternal:           "Internal Server Error",
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

// errResponse maps a domain or infrastructure error to an RFC-9457 HTTP response.
func errResponse(c *gin.Context, err error) {
	var valErr *domain.ValidationFailedError
	switch {
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, pgx.ErrNoRows):
		writeProblem(c, http.StatusNotFound, CodeNotFound, err.Error(), nil)
	case errors.Is(err, domain.ErrNoDraftExists):
		writeProblem(c, http.StatusNotFound, CodeDraftNotFound, err.Error(), nil)
	case errors.Is(err, domain.ErrNoActiveVersion):
		writeProblem(c, http.StatusNotFound, CodeNoActiveVersion, err.Error(), nil)
	case errors.Is(err, domain.ErrUnauthorized):
		writeProblem(c, http.StatusUnauthorized, CodeUnauthorized, err.Error(), nil)
	case errors.Is(err, domain.ErrForbidden):
		writeProblem(c, http.StatusForbidden, CodeForbidden, err.Error(), nil)
	case errors.Is(err, domain.ErrDraftAlreadyExists):
		writeProblem(c, http.StatusConflict, CodeDraftAlreadyExists, err.Error(), nil)
	case errors.Is(err, domain.ErrDuplicateBusinessKey):
		writeProblem(c, http.StatusConflict, CodeDuplicateKey, err.Error(), nil)
	case errors.Is(err, domain.ErrDraftConcurrency):
		writeProblem(c, http.StatusConflict, CodeDraftConcurrency, err.Error(), nil)
	case errors.Is(err, domain.ErrInvalidVersionStatus),
		errors.Is(err, domain.ErrVersionNotDraft),
		errors.Is(err, domain.ErrVersionNotPublished),
		errors.Is(err, domain.ErrVersionAlreadyPublished):
		writeProblem(c, http.StatusConflict, CodeInvalidStatus, err.Error(), nil)
	case errors.Is(err, domain.ErrActiveInstancesExist):
		writeProblem(c, http.StatusConflict, CodeActiveInstances, err.Error(), nil)
	case errors.Is(err, domain.ErrStructuralDivergence):
		writeProblem(c, http.StatusConflict, CodeStructuralDiv, err.Error(), nil)
	case errors.Is(err, domain.ErrIdempotencyKeyReplay):
		writeProblem(c, http.StatusConflict, CodeIdempotencyReplay, err.Error(), nil)
	case errors.Is(err, domain.ErrPlanQuotaExceeded):
		writeProblem(c, http.StatusUnprocessableEntity, CodePlanQuota, err.Error(), nil)
	case errors.Is(err, domain.ErrAssigneeIneligible):
		writeProblem(c, http.StatusUnprocessableEntity, CodeAssigneeIneligible, err.Error(), nil)
	case errors.As(err, &valErr):
		params := make([]InvalidParam, len(valErr.Errors))
		for i, e := range valErr.Errors {
			params[i] = InvalidParam{Name: e.NodeID, Reason: e.Message, Code: ErrCode(e.Code)}
		}
		writeProblem(c, http.StatusUnprocessableEntity, CodeBPMNValidation,
			"BPMN semantic or structural validation failed.", params)
	case errors.Is(err, domain.ErrUpstreamUnavailable):
		writeProblem(c, http.StatusServiceUnavailable, CodeUpstream, err.Error(), nil)
	default:
		writeProblem(c, http.StatusInternalServerError, CodeInternal,
			"an unexpected error occurred", nil)
	}
}
