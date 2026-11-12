package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
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
	CodeModuleInUse          ErrCode = "MODULE_IN_USE"
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
	CodeMethodNotAllowed     ErrCode = "METHOD_NOT_ALLOWED"
)

type invalidParam struct {
	Name   string  `json:"name"`
	Reason string  `json:"reason"`
	Code   ErrCode `json:"code,omitempty"`
}

type moduleUseParam struct {
	WorkflowID    uuid.UUID            `json:"workflow_id"`
	WorkflowKey   string               `json:"workflow_key"`
	Status        domain.VersionStatus `json:"status"`
	VersionNumber *int32               `json:"version_number,omitempty"`
}

type problemDetails struct {
	Type                 string           `json:"type"`
	Title                string           `json:"title"`
	Status               int              `json:"status"`
	Detail               string           `json:"detail"`
	Instance             string           `json:"instance"`
	Code                 ErrCode          `json:"code"`
	TraceID              string           `json:"trace_id,omitempty"`
	RequestID            string           `json:"request_id,omitempty"`
	CurrentRecordVersion *int64           `json:"current_record_version,omitempty"`
	InvalidParams        []invalidParam   `json:"invalid_params,omitempty"`
	Uses                 []moduleUseParam `json:"uses,omitempty"`
	PlanCode             string           `json:"plan_code,omitempty"`
	Limit                *int64           `json:"limit,omitempty"`
	CurrentCount         *int64           `json:"current_count,omitempty"`
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
	http.StatusMethodNotAllowed:      errBase + "method-not-allowed",
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
	CodeModuleInUse:          "Module In Use",
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
	CodeMethodNotAllowed:     "Method Not Allowed",
}

func writeProblem(c *gin.Context, status int, code ErrCode, detail string, params []invalidParam) {
	writeProblemWithVersion(c, status, code, detail, params, nil)
}

func writeProblemWithVersion(c *gin.Context, status int, code ErrCode, detail string, params []invalidParam, currentRecordVersion *int64) {
	p := newProblem(c, status, code, detail, params)
	p.CurrentRecordVersion = currentRecordVersion
	writeProblemDocument(c, p)
}

func writeProblemWithUses(c *gin.Context, status int, code ErrCode, detail string, uses []domain.ModuleUse) {
	p := newProblem(c, status, code, detail, nil)
	p.Uses = make([]moduleUseParam, len(uses))
	for i, u := range uses {
		p.Uses[i] = moduleUseParam{WorkflowID: u.WorkflowID, WorkflowKey: u.WorkflowKey, Status: u.Status, VersionNumber: u.VersionNumber}
	}
	writeProblemDocument(c, p)
}

func writeProblemWithQuota(c *gin.Context, status int, code ErrCode, detail string, quota *domain.PlanQuotaError) {
	p := newProblem(c, status, code, detail, nil)
	p.PlanCode = quota.Plan
	p.Limit = &quota.Limit
	p.CurrentCount = &quota.Count
	writeProblemDocument(c, p)
}

func writeProblemDocument(c *gin.Context, p problemDetails) {
	c.Header("Content-Type", problemContentType)
	c.JSON(p.Status, p)
}

func newProblem(c *gin.Context, status int, code ErrCode, detail string, params []invalidParam) problemDetails {
	typeURI, ok := problemTypes[status]
	if !ok {
		typeURI = errBase + "internal-error"
	}
	return problemDetails{
		Type:          typeURI,
		Title:         codeTitles[code],
		Status:        status,
		Detail:        detail,
		Instance:      c.Request.URL.Path,
		Code:          code,
		TraceID:       c.Writer.Header().Get("X-Trace-ID"),
		RequestID:     c.Writer.Header().Get("X-Request-ID"),
		InvalidParams: params,
	}
}

type lengthLimit struct {
	field    string
	value    string
	max      int
	notBlank bool
}

func rejectInvalidText(c *gin.Context, limits ...lengthLimit) bool {
	var invalids []invalidParam
	var tooLong, hasNul, hasBlank bool
	for _, l := range limits {
		switch {
		case l.notBlank && strings.TrimSpace(l.value) == "":
			hasBlank = true
			invalids = append(invalids, invalidParam{Name: l.field, Reason: "must not be blank"})
		case strings.ContainsRune(l.value, 0):
			hasNul = true
			invalids = append(invalids, invalidParam{Name: l.field, Reason: "must not contain NUL characters"})
		case utf8.RuneCountInString(l.value) > l.max:
			tooLong = true
			invalids = append(invalids, invalidParam{Name: l.field, Reason: fmt.Sprintf("must not exceed %d characters", l.max)})
		}
	}
	if len(invalids) == 0 {
		return false
	}
	detail := "request fields exceed maximum length"
	switch {
	case hasBlank && (hasNul || tooLong):
		detail = "request fields are blank, exceed maximum length or contain NUL characters"
	case hasBlank:
		detail = "request fields must not be blank"
	case hasNul && tooLong:
		detail = "request fields exceed maximum length or contain NUL characters"
	case hasNul:
		detail = "request fields must not contain NUL characters"
	}
	writeProblem(c, http.StatusUnprocessableEntity, CodeInvalidInput, detail, invalids)
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
	var unknown *unknownFieldsError
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
		name := wrongType.Field
		if name == "" {
			name = "body"
		}
		return "request body has a field of the wrong type", []invalidParam{{Name: name, Reason: "has the wrong type"}}
	case errors.As(err, &unknown):
		params := make([]invalidParam, 0, len(unknown.names))
		for _, name := range unknown.names {
			params = append(params, invalidParam{Name: name, Reason: "is not a known field"})
		}
		return "request body has fields this endpoint does not define", params
	default:
		return "request body is not valid JSON", nil
	}
}

func NotFound(c *gin.Context) {
	writeProblem(c, http.StatusNotFound, CodeNotFound, "no route matches this method and path", nil)
}

func MethodNotAllowed(c *gin.Context) {
	writeProblem(c, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "the method is not allowed for this path", nil)
}

func SpecUnavailable(c *gin.Context) {
	writeProblem(c, http.StatusInternalServerError, CodeInternal, "the AsyncAPI document is unavailable", nil)
}

func errResponse(c *gin.Context, log port.Logger, err error) {
	status, code, detail, params := mapErr(err)
	if log != nil && status >= 500 && !errors.Is(err, context.Canceled) {
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
			"trace_id":  gincommon.SpanTraceID(c.Request.Context()),
		})
	}
	var conflict *domain.DraftConcurrencyError
	if errors.As(err, &conflict) {
		writeProblemWithVersion(c, status, code, detail, params, &conflict.Current)
		return
	}
	var inUse *domain.ModuleInUseError
	if errors.As(err, &inUse) {
		writeProblemWithUses(c, status, code, detail, inUse.Uses)
		return
	}
	var quota *domain.PlanQuotaError
	if errors.As(err, &quota) {
		writeProblemWithQuota(c, status, code, detail, quota)
		return
	}
	writeProblem(c, status, code, detail, params)
}

func mapErr(err error) (status int, code ErrCode, detail string, params []invalidParam) {
	var valErr *domain.ValidationFailedError
	switch {
	case errors.Is(err, domain.ErrUpstreamUnavailable):
		return http.StatusServiceUnavailable, CodeUpstream, "an upstream dependency is temporarily unavailable", nil
	case errors.Is(err, domain.ErrNotFound), errors.Is(err, pgcommon.ErrNoRows):
		return http.StatusNotFound, CodeNotFound, "resource not found", nil
	case errors.Is(err, domain.ErrNoDraftExists):
		return http.StatusNotFound, CodeDraftNotFound, domain.ErrNoDraftExists.Error(), nil
	case errors.Is(err, domain.ErrNoActiveVersion):
		return http.StatusConflict, CodeNoActiveVersion, domain.ErrNoActiveVersion.Error(), nil
	case errors.Is(err, domain.ErrUnauthorized):
		return http.StatusUnauthorized, CodeUnauthorized, err.Error(), nil
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden, CodeForbidden, err.Error(), nil
	case errors.Is(err, domain.ErrDraftAlreadyExists):
		return http.StatusConflict, CodeDraftAlreadyExists, domain.ErrDraftAlreadyExists.Error(), nil
	case errors.Is(err, domain.ErrDuplicateTemplateKey):
		return http.StatusConflict, CodeDuplicateKey, "a workflow with this key already exists", nil
	case errors.Is(err, domain.ErrRecordVersionRequired):
		return http.StatusBadRequest, CodeBadRequest, "request is missing its optimistic-lock token", []invalidParam{{Name: "record_version", Reason: "is required"}}
	case errors.Is(err, domain.ErrDraftConcurrency):
		return http.StatusConflict, CodeDraftConcurrency, domain.ErrDraftConcurrency.Error(), nil
	case errors.Is(err, domain.ErrInvalidVersionStatus),
		errors.Is(err, domain.ErrVersionNotDraft),
		errors.Is(err, domain.ErrVersionNotPublished),
		errors.Is(err, domain.ErrVersionAlreadyPublished):
		return http.StatusConflict, CodeInvalidStatus, matchedSentinel(err, domain.ErrVersionNotDraft, domain.ErrVersionNotPublished, domain.ErrVersionAlreadyPublished, domain.ErrInvalidVersionStatus), nil
	case errors.Is(err, domain.ErrActiveInstancesExist):
		return http.StatusConflict, CodeActiveInstances, domain.ErrActiveInstancesExist.Error(), nil
	case errors.Is(err, domain.ErrStructuralDivergence):
		return http.StatusConflict, CodeStructuralDiv, domain.ErrStructuralDivergence.Error(), nil
	case errors.Is(err, domain.ErrModuleProcessIDTaken), errors.Is(err, domain.ErrModuleProcessIDChanged):
		return http.StatusConflict, CodeModuleProcessID, matchedSentinel(err, domain.ErrModuleProcessIDTaken, domain.ErrModuleProcessIDChanged), nil
	case errors.Is(err, domain.ErrModuleInUse):
		return http.StatusConflict, CodeModuleInUse,
			"the module is used by published workflow versions or workflow drafts; remove those uses before archiving it", nil
	case errors.Is(err, domain.ErrIdempotencyKeyReplay):
		return http.StatusConflict, CodeIdempotencyReplay, domain.ErrIdempotencyKeyReplay.Error(), nil
	case errors.Is(err, domain.ErrIdempotencyInProgress):
		return http.StatusConflict, CodeIdempotencyRunning, domain.ErrIdempotencyInProgress.Error(), nil
	case errors.Is(err, domain.ErrPlanQuotaExceeded):
		return http.StatusForbidden, CodePlanQuota,
			"the workflow template limit of the plan has been reached; archive a workflow that is no longer used or move to a larger plan", nil
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
	case errors.Is(err, domain.ErrMalformedBPMN):
		return http.StatusBadRequest, CodeInvalidBPMNXML, "the BPMN document could not be parsed", nil
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return http.StatusServiceUnavailable, CodeUpstream, "the request was cancelled or its deadline passed", nil
	default:
		return http.StatusInternalServerError, CodeInternal, "an unexpected error occurred", nil
	}
}

func matchedSentinel(err error, sentinels ...error) string {
	for _, s := range sentinels {
		if errors.Is(err, s) {
			return s.Error()
		}
	}
	return sentinels[0].Error()
}

var errTrailingData = errors.New("request body has data after its JSON value")

const maxEchoedFieldName = 64

func bindStrictJSON(c *gin.Context, obj any) error {
	if c.Request == nil || c.Request.Body == nil {
		return errors.New("invalid request")
	}
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return fmt.Errorf("read request body: %w", err)
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return fmt.Errorf("decode request body: %w", io.EOF)
	}
	if err := checkKnownFields(body, obj); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(obj); err != nil {
		return fmt.Errorf("decode request body: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errTrailingData
	}
	if err := binding.Validator.ValidateStruct(obj); err != nil {
		return fmt.Errorf("validate request body: %w", err)
	}
	return nil
}

func recordVersionFromIfMatch(header string) (int64, bool) {
	value := strings.TrimSpace(header)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
