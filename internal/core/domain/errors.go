package domain

import "errors"

var (
	ErrNotFound                        = errors.New("resource not found")
	ErrForbidden                       = errors.New("forbidden")
	ErrUnauthorized                    = errors.New("unauthorized")
	ErrDraftAlreadyExists              = errors.New("draft already exists")
	ErrNoDraftExists                   = errors.New("no active draft exists")
	ErrNoActiveVersion                 = errors.New("no active version")
	ErrDuplicateBusinessKey            = errors.New("workflow business key already exists")
	ErrDraftConcurrency                = errors.New("draft concurrency violation")
	ErrInvalidVersionStatus            = errors.New("invalid version status for this operation")
	ErrVersionNotDraft                 = errors.New("workflow version is not in DRAFT status")
	ErrVersionNotPublished             = errors.New("workflow version is not in PUBLISHED status")
	ErrVersionAlreadyPublished         = errors.New("workflow version has already been published")
	ErrActiveInstancesExist            = errors.New("active workflow instances exist")
	ErrStructuralDivergence            = errors.New("structural divergence detected")
	ErrPlanQuotaExceeded               = errors.New("plan quota exceeded")
	ErrAssigneeIneligible              = errors.New("assignee ineligible")
	ErrUpstreamUnavailable             = errors.New("upstream service unavailable")
	ErrIdempotencyKeyReplay            = errors.New("idempotency key replay with different payload")
	ErrIdempotencyInProgress           = errors.New("a request with this idempotency key is still running")
	ErrMalformedBPMN                   = errors.New("malformed BPMN document")
	ErrForbiddenXML                    = errors.New("forbidden XML construct")
	ErrInvalidConnectorCredentialInput = errors.New("invalid connector credential input")
	ErrInvalidConnectorAliasInput      = errors.New("invalid connector alias input")
	ErrModuleProcessIDTaken            = errors.New("module process id is already in use")
	ErrModuleProcessIDChanged          = errors.New("module version must keep its module's process id")
)

type BPMNErrorCode string

const (
	BPMNErrRejectedElement                BPMNErrorCode = "REJECTED_ELEMENT"
	BPMNErrUnsupportedElement             BPMNErrorCode = "UNSUPPORTED_ELEMENT"
	BPMNErrMissingNamespace               BPMNErrorCode = "MISSING_NAMESPACE"
	BPMNErrMissingTaskDefinition          BPMNErrorCode = "MISSING_TASK_DEFINITION"
	BPMNErrMissingAssignmentDefinition    BPMNErrorCode = "MISSING_ASSIGNMENT_DEFINITION"
	BPMNErrInvalidTaskDefinitionType      BPMNErrorCode = "INVALID_TASK_DEFINITION_TYPE"
	BPMNWarnUnknownStageType              BPMNErrorCode = "UNKNOWN_STAGE_TYPE"
	BPMNWarnUnknownConnectorType          BPMNErrorCode = "UNKNOWN_CONNECTOR_TYPE"
	BPMNWarnGatewayBranchWithoutCondition BPMNErrorCode = "GATEWAY_BRANCH_WITHOUT_CONDITION"
	BPMNWarnConnectorTaskHasAssignment    BPMNErrorCode = "CONNECTOR_TASK_HAS_ASSIGNMENT"
	BPMNErrConnectorSecretAuthored        BPMNErrorCode = "CONNECTOR_SECRET_AUTHORED"
	BPMNErrTaskNotInLane                  BPMNErrorCode = "TASK_NOT_IN_LANE"
	BPMNErrCandidateGroupsEmpty           BPMNErrorCode = "CANDIDATE_GROUPS_EMPTY"
	BPMNErrCandidateUsersEmpty            BPMNErrorCode = "CANDIDATE_USERS_EMPTY"
	BPMNErrInvalidCandidateUser           BPMNErrorCode = "INVALID_CANDIDATE_USER"
	BPMNErrInvalidSLADuration             BPMNErrorCode = "INVALID_SLA_DURATION"
	BPMNErrInvalidConditionExpression     BPMNErrorCode = "INVALID_CONDITION_EXPRESSION"
	BPMNErrMissingMessageDefinition       BPMNErrorCode = "MISSING_MESSAGE_DEFINITION"
	BPMNErrUnmatchedMessageFlow           BPMNErrorCode = "UNMATCHED_MESSAGE_FLOW"
	BPMNErrTaskLimitExceeded              BPMNErrorCode = "TASK_LIMIT_EXCEEDED"
	BPMNErrLaneLimitExceeded              BPMNErrorCode = "LANE_LIMIT_EXCEEDED"
	BPMNErrMultipleStartEvents            BPMNErrorCode = "MULTIPLE_START_EVENTS"
	BPMNErrMultipleEndEvents              BPMNErrorCode = "MULTIPLE_END_EVENTS"
	BPMNErrNoStartEvent                   BPMNErrorCode = "NO_START_EVENT"
	BPMNErrNoEndEvent                     BPMNErrorCode = "NO_END_EVENT"
	BPMNErrDanglingNode                   BPMNErrorCode = "DANGLING_NODE"
	BPMNErrUnreachableNode                BPMNErrorCode = "UNREACHABLE_NODE"
	BPMNErrCycleDetected                  BPMNErrorCode = "CYCLE_DETECTED"
	BPMNErrUnguardedLoop                  BPMNErrorCode = "UNGUARDED_LOOP"
	BPMNErrMaxDepthExceeded               BPMNErrorCode = "MAX_DEPTH_EXCEEDED"
	BPMNErrUnmatchedGateway               BPMNErrorCode = "UNMATCHED_GATEWAY"
	BPMNErrInvalidSequenceFlowRef         BPMNErrorCode = "INVALID_SEQUENCE_FLOW_REF"
	BPMNErrMultipleProcesses              BPMNErrorCode = "MULTIPLE_PROCESSES"
	BPMNErrNoRunnableProcess              BPMNErrorCode = "NO_RUNNABLE_PROCESS"
	BPMNErrIgnoredLaneWork                BPMNErrorCode = "IGNORED_LANE_WORK"
	BPMNErrInvalidBoundaryAttachment      BPMNErrorCode = "INVALID_BOUNDARY_ATTACHMENT"
	BPMNErrUnsupportedBoundaryTarget      BPMNErrorCode = "UNSUPPORTED_BOUNDARY_TARGET"
	BPMNErrMissingDiagram                 BPMNErrorCode = "MISSING_DIAGRAM"
	BPMNErrMissingDiagramShape            BPMNErrorCode = "MISSING_DIAGRAM_SHAPE"
	BPMNErrInvalidZeebeProperty           BPMNErrorCode = "INVALID_ZEEBE_PROPERTY"
	BPMNErrUnresolvedCalledElement        BPMNErrorCode = "UNRESOLVED_CALLED_ELEMENT"
	BPMNErrNestedSubProcessNotSupported   BPMNErrorCode = "NESTED_SUBPROCESS_NOT_SUPPORTED"
	BPMNErrMissingDeptInputForModule      BPMNErrorCode = "MISSING_DEPT_INPUT_FOR_MODULE"
	BPMNErrMissingDeptID                  BPMNErrorCode = "MISSING_DEPT_ID"
	BPMNErrInvalidDeptID                  BPMNErrorCode = "INVALID_DEPT_ID"
	BPMNErrInvalidLaneName                BPMNErrorCode = "INVALID_LANE_NAME"
	BPMNErrCalledElementCycle             BPMNErrorCode = "CALLED_ELEMENT_CYCLE"
	BPMNErrDuplicateProcessID             BPMNErrorCode = "DUPLICATE_PROCESS_ID"
	BPMNErrSplitWithoutGateway            BPMNErrorCode = "SPLIT_WITHOUT_GATEWAY"
	BPMNErrEndEventWithOutgoingFlow       BPMNErrorCode = "END_EVENT_WITH_OUTGOING_FLOW"
	BPMNErrDuplicateElementID             BPMNErrorCode = "DUPLICATE_ELEMENT_ID"
	BPMNErrInvalidTaskSchedule            BPMNErrorCode = "INVALID_TASK_SCHEDULE"
)

type BPMNValidationSeverity string

const (
	SeverityError   BPMNValidationSeverity = "error"
	SeverityWarning BPMNValidationSeverity = "warning"
)

type BPMNValidationError struct {
	Code     BPMNErrorCode
	NodeID   string
	Message  string
	Severity BPMNValidationSeverity
}

func (e *BPMNValidationError) Error() string {
	return string(e.Code) + ": " + e.Message
}

type ValidationFailedError struct {
	Errors []BPMNValidationError
}

func (e *ValidationFailedError) Error() string {
	return "BPMN validation failed"
}
