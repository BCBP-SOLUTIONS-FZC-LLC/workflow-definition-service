package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
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
	CodeDuplicateKey         ErrCode = "DUPLICATE_TEMPLATE_KEY"
	CodeDraftConcurrency     ErrCode = "DRAFT_CONCURRENCY"
	CodeInvalidStatus        ErrCode = "INVALID_VERSION_STATUS"
	CodeActiveInstances      ErrCode = "ACTIVE_INSTANCES_EXIST"
	CodeStructuralDiv        ErrCode = "STRUCTURAL_DIVERGENCE"
	CodeModuleProcessID      ErrCode = "MODULE_PROCESS_ID_CONFLICT"
	CodePlanQuota            ErrCode = "PLAN_QUOTA_EXCEEDED"
	CodeAssigneeIneligible   ErrCode = "ASSIGNEE_INELIGIBLE"
	CodeUpstream             ErrCode = "UPSTREAM_UNAVAILABLE"
	CodeIdempotencyReplay    ErrCode = "IDEMPOTENCY_KEY_REPLAY"
	CodeIdempotencyRunning   ErrCode = "IDEMPOTENCY_REQUEST_IN_PROGRESS"
	CodeBPMNValidation       ErrCode = "BPMN_VALIDATION_FAILED"
	CodeInvalidBPMNXML       ErrCode = "INVALID_BPMN_XML"
	CodePayloadTooLarge      ErrCode = "PAYLOAD_TOO_LARGE"
	CodeUnsupportedMediaType ErrCode = "UNSUPPORTED_MEDIA_TYPE"
	CodeInternal             ErrCode = "INTERNAL_ERROR"
	CodeInvalidInput         ErrCode = "INVALID_INPUT"
	CodeEventDecodeFailed    ErrCode = "EVENT_DECODE_FAILED"
)

type invalidParam struct {
	Name   string  `json:"name"`
	Reason string  `json:"reason"`
	Code   ErrCode `json:"code,omitempty"`
}

type problemDetails struct {
	Type                 string         `json:"type"`
	Title                string         `json:"title"`
	Status               int            `json:"status"`
	Detail               string         `json:"detail"`
	Instance             string         `json:"instance"`
	Code                 ErrCode        `json:"code"`
	TraceID              string         `json:"trace_id,omitempty"`
	RequestID            string         `json:"request_id,omitempty"`
	CurrentRecordVersion *int64         `json:"current_record_version,omitempty"`
	InvalidParams        []invalidParam `json:"invalid_params,omitempty"`
}

const (
	errBase            = "https://api.workflow.platform/errors/"
	problemContentType = "application/problem+json"
)

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
	http.StatusBadGateway:            errBase + "bad-gateway",
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
	CodeDuplicateKey:         "Duplicate Template Key",
	CodeDraftConcurrency:     "Draft Concurrency Violation",
	CodeInvalidStatus:        "Invalid Version Status",
	CodeActiveInstances:      "Active Instances Exist",
	CodeStructuralDiv:        "Structural Divergence",
	CodeModuleProcessID:      "Module Process Id Conflict",
	CodePlanQuota:            "Plan Quota Exceeded",
	CodeAssigneeIneligible:   "Assignee Ineligible",
	CodeUpstream:             "Upstream Service Unavailable",
	CodeIdempotencyReplay:    "Idempotency Key Replay",
	CodeIdempotencyRunning:   "Idempotency Request In Progress",
	CodeBPMNValidation:       "BPMN Validation Failed",
	CodeInvalidBPMNXML:       "Invalid BPMN XML",
	CodePayloadTooLarge:      "Payload Too Large",
	CodeUnsupportedMediaType: "Unsupported Media Type",
	CodeInternal:             "Internal Server Error",
	CodeInvalidInput:         "Invalid Input",
	CodeEventDecodeFailed:    "Event Decode Failed",
}

func writeProblem(c *gin.Context, status int, code ErrCode, detail string, params []invalidParam) {
	writeProblemWithVersion(c, status, code, detail, params, nil)
}

func writeProblemWithVersion(c *gin.Context, status int, code ErrCode, detail string, params []invalidParam, currentRecordVersion *int64) {
	typeURI, ok := problemTypes[status]
	if !ok {
		typeURI = errBase + "internal-error"
	}
	c.Header("Content-Type", problemContentType)
	c.JSON(status, problemDetails{
		Type:                 typeURI,
		Title:                codeTitles[code],
		Status:               status,
		Detail:               detail,
		Instance:             c.Request.URL.Path,
		Code:                 code,
		TraceID:              c.Writer.Header().Get("X-Trace-ID"),
		RequestID:            c.Writer.Header().Get("X-Request-ID"),
		CurrentRecordVersion: currentRecordVersion,
		InvalidParams:        params,
	})
}

type lengthLimit struct {
	field string
	value string
	max   int
}

func rejectTooLong(c *gin.Context, limits ...lengthLimit) bool {
	var invalids []invalidParam
	for _, l := range limits {
		if utf8.RuneCountInString(l.value) > l.max {
			invalids = append(invalids, invalidParam{Name: l.field, Reason: fmt.Sprintf("must not exceed %d characters", l.max)})
		}
	}
	if len(invalids) == 0 {
		return false
	}
	writeProblem(c, http.StatusUnprocessableEntity, CodeInvalidInput, "request fields exceed maximum length", invalids)
	return true
}

func writeIfTooLarge(c *gin.Context, err error) bool {
	var maxBytesErr *http.MaxBytesError
	if !errors.As(err, &maxBytesErr) {
		return false
	}
	writeProblem(c, http.StatusRequestEntityTooLarge, CodePayloadTooLarge,
		"request body exceeds the 10 MB limit", nil)
	return true
}

func (h *Handler) bindErrResponse(c *gin.Context, err error) {
	if writeIfTooLarge(c, err) {
		return
	}
	if h.log != nil {
		h.log.Warn("invalid request body", map[string]any{"path": c.FullPath(), "error": err.Error()})
	}
	detail, params := describeBindError(err)
	writeProblem(c, http.StatusBadRequest, CodeBadRequest, detail, params)
}

func describeBindError(err error) (string, []invalidParam) {
	var invalid validator.ValidationErrors
	var wrongType *json.UnmarshalTypeError
	switch {
	case errors.As(err, &invalid):
		params := make([]invalidParam, 0, len(invalid))
		for _, fe := range invalid {
			reason := "is not valid"
			if fe.Tag() == "required" {
				reason = "is required"
			}
			params = append(params, invalidParam{Name: fe.Field(), Reason: reason})
		}
		return "request body has missing or invalid fields", params
	case errors.As(err, &wrongType):
		return "request body has a field of the wrong type", []invalidParam{{Name: wrongType.Field, Reason: "has the wrong type"}}
	case unknownFieldName(err) != "":
		return "request body has a field this endpoint does not define", []invalidParam{{Name: unknownFieldName(err), Reason: "is not a known field"}}
	default:
		return "request body is not valid JSON", nil
	}
}

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
	var conflict *domain.DraftConcurrencyError
	if errors.As(err, &conflict) {
		writeProblemWithVersion(c, status, code, detail, params, &conflict.Current)
		return
	}
	writeProblem(c, status, code, detail, params)
}

func mapErr(err error) (status int, code ErrCode, detail string, params []invalidParam) {
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
	case errors.Is(err, domain.ErrDuplicateTemplateKey):
		return http.StatusConflict, CodeDuplicateKey, err.Error(), nil
	case errors.Is(err, domain.ErrRecordVersionRequired):
		return http.StatusBadRequest, CodeBadRequest, "request is missing its optimistic-lock token", []invalidParam{{Name: "record_version", Reason: "is required"}}
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
	case errors.Is(err, domain.ErrModuleProcessIDTaken), errors.Is(err, domain.ErrModuleProcessIDChanged):
		return http.StatusConflict, CodeModuleProcessID, err.Error(), nil
	case errors.Is(err, domain.ErrIdempotencyKeyReplay):
		return http.StatusConflict, CodeIdempotencyReplay, err.Error(), nil
	case errors.Is(err, domain.ErrIdempotencyInProgress):
		return http.StatusConflict, CodeIdempotencyRunning, err.Error(), nil
	case errors.Is(err, domain.ErrPlanQuotaExceeded):
		return http.StatusForbidden, CodePlanQuota, err.Error(), nil
	case errors.Is(err, domain.ErrAssigneeIneligible):
		return http.StatusUnprocessableEntity, CodeAssigneeIneligible, err.Error(), nil
	case errors.Is(err, domain.ErrInvalidConnectorCredentialInput):
		return http.StatusBadRequest, CodeInvalidInput, err.Error(), nil
	case errors.Is(err, domain.ErrInvalidConnectorAliasInput):
		return http.StatusBadRequest, CodeInvalidInput, err.Error(), nil
	case errors.As(err, &valErr):
		p := make([]invalidParam, len(valErr.Errors))
		for i, e := range valErr.Errors {
			p[i] = invalidParam{Name: e.NodeID, Reason: e.Message, Code: ErrCode(e.Code)}
		}
		return http.StatusUnprocessableEntity, CodeBPMNValidation,
			"BPMN semantic or structural validation failed.", p
	case errors.Is(err, domain.ErrUpstreamUnavailable):
		return http.StatusServiceUnavailable, CodeUpstream, "an upstream dependency is temporarily unavailable", nil
	case errors.Is(err, domain.ErrMalformedBPMN):
		return http.StatusBadRequest, CodeInvalidBPMNXML, "the BPMN document could not be parsed", nil
	default:
		return http.StatusInternalServerError, CodeInternal, "an unexpected error occurred", nil
	}
}

const unknownFieldPrefix = "json: unknown field "

const maxEchoedFieldName = 64

func unknownFieldName(err error) string {
	msg := err.Error()
	at := strings.Index(msg, unknownFieldPrefix)
	if at < 0 {
		return ""
	}
	name, unquoteErr := strconv.Unquote(msg[at+len(unknownFieldPrefix):])
	if unquoteErr != nil || name == "" {
		return ""
	}
	if utf8.RuneCountInString(name) > maxEchoedFieldName {
		name = string([]rune(name)[:maxEchoedFieldName])
	}
	return name
}

func bindStrictJSON(c *gin.Context, obj any) error {
	if c.Request == nil || c.Request.Body == nil {
		return errors.New("invalid request")
	}
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(obj); err != nil {
		return fmt.Errorf("decode request body: %w", err)
	}
	if err := binding.Validator.ValidateStruct(obj); err != nil {
		return fmt.Errorf("validate request body: %w", err)
	}
	return nil
}

func recordVersionFromIfMatch(header string) (int64, bool) {
	value := strings.TrimPrefix(strings.TrimSpace(header), "W/")
	value = strings.Trim(value, `"`)
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
