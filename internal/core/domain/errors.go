package domain

import "errors"

var (
	ErrNotFound             = errors.New("resource not found")
	ErrForbidden            = errors.New("forbidden")
	ErrUnauthorized         = errors.New("unauthorized")
	ErrDraftAlreadyExists   = errors.New("draft already exists")
	ErrNoDraftExists        = errors.New("no active draft exists")
	ErrNoActiveVersion      = errors.New("no active version")
	ErrDuplicateBusinessKey = errors.New("workflow business key already exists")
	ErrDraftConcurrency     = errors.New("draft concurrency violation")
	ErrInvalidVersionStatus = errors.New("invalid version status for this operation")
	ErrActiveInstancesExist = errors.New("active workflow instances exist")
	ErrStructuralDivergence = errors.New("structural divergence detected")
	ErrPlanQuotaExceeded    = errors.New("plan quota exceeded")
	ErrAssigneeIneligible   = errors.New("assignee ineligible")
	ErrUpstreamUnavailable  = errors.New("upstream service unavailable")
	ErrIdempotencyKeyReplay = errors.New("idempotency key replay with different payload")
)

type BPMNErrorCode string

const (
	BPMNErrRejectedElement      BPMNErrorCode = "REJECTED_ELEMENT"
	BPMNErrMissingNamespace     BPMNErrorCode = "MISSING_NAMESPACE"
	BPMNErrMissingZeebeProperty BPMNErrorCode = "MISSING_ZEEBE_PROPERTY"
	BPMNErrInvalidDeptID        BPMNErrorCode = "INVALID_DEPT_ID"
	BPMNErrInvalidStageType     BPMNErrorCode = "INVALID_STAGE_TYPE"
	// BPMNErrRoleEmpty fires when the role property exceeds maxRoleLen (64 chars).
	// An absent or empty role emits BPMNErrMissingZeebeProperty instead.
	BPMNErrRoleEmpty                 BPMNErrorCode = "ROLE_EMPTY"
	BPMNErrInvalidUUID               BPMNErrorCode = "INVALID_UUID"
	BPMNErrTaskAssigneeLimitExceeded BPMNErrorCode = "TASK_ASSIGNEE_LIMIT_EXCEEDED"
	BPMNErrTaskLimitExceeded         BPMNErrorCode = "TASK_LIMIT_EXCEEDED"
	BPMNErrLaneLimitExceeded         BPMNErrorCode = "LANE_LIMIT_EXCEEDED"
	BPMNErrMultipleStartEvents       BPMNErrorCode = "MULTIPLE_START_EVENTS"
	BPMNErrMultipleEndEvents         BPMNErrorCode = "MULTIPLE_END_EVENTS"
	BPMNErrNoStartEvent              BPMNErrorCode = "NO_START_EVENT"
	BPMNErrNoEndEvent                BPMNErrorCode = "NO_END_EVENT"
	BPMNErrDanglingNode              BPMNErrorCode = "DANGLING_NODE"
	BPMNErrUnreachableNode           BPMNErrorCode = "UNREACHABLE_NODE"
	BPMNErrCycleDetected             BPMNErrorCode = "CYCLE_DETECTED"
	BPMNErrUnmatchedGateway          BPMNErrorCode = "UNMATCHED_GATEWAY"
	BPMNErrInvalidSequenceFlowRef    BPMNErrorCode = "INVALID_SEQUENCE_FLOW_REF"
	BPMNErrMultipleProcesses         BPMNErrorCode = "MULTIPLE_PROCESSES"
	BPMNErrInvalidSLADuration        BPMNErrorCode = "INVALID_SLA_DURATION"
	BPMNErrInvalidZeebeProperty      BPMNErrorCode = "INVALID_ZEEBE_PROPERTY"
)

type BPMNValidationError struct {
	Code    BPMNErrorCode
	NodeID  string
	Message string
}

func (e *BPMNValidationError) Error() string {
	return string(e.Code) + ": " + e.Message
}

// ValidationFailedError is returned when BPMN structural or semantic validation fails.
// It carries the full list of per-node errors so the handler can render invalid_params.
type ValidationFailedError struct {
	Errors []BPMNValidationError
}

func (e *ValidationFailedError) Error() string {
	return "BPMN validation failed"
}
