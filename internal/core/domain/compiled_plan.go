package domain

type CompiledPlan struct {
	Name           string             `json:"name"`
	TaskQueue      string             `json:"task_queue,omitempty"`
	Ignored        bool               `json:"ignored,omitempty"`
	Departments    []DepartmentDef    `json:"departments"`
	Execution      ExecutionPlan      `json:"execution"`
	VisualElements []VisualElementDef `json:"visual_elements,omitempty"`
}

// VisualElementDef represents a diagram-only BPMN element (e.g. dataStoreReference) with no execution semantics.
type VisualElementDef struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

type DepartmentDef struct {
	ID     string            `json:"id"`
	Label  string            `json:"label"`
	Ignore bool              `json:"ignore,omitempty"`
	Props  map[string]string `json:"props,omitempty"`
	Stages []StageDef        `json:"stages"`
}

type StageDef struct {
	Type             string            `json:"type"`
	Activity         string            `json:"activity"`
	NodeID           string            `json:"node_id,omitempty"`
	Role             string            `json:"role"`
	DefaultAssignees []string          `json:"default_assignees,omitempty"`
	DueDate          string            `json:"due_date,omitempty"`
	FollowUpDate     string            `json:"follow_up_date,omitempty"`
	BoundaryTimer    *BoundaryTimer    `json:"boundary_timer,omitempty"`
	BoundaryMessage  *MessagePath      `json:"boundary_message,omitempty"`
	EngineNote       string            `json:"engine_note,omitempty"`
	Extras           map[string]string `json:"extras,omitempty"`
	IsZeebeUserTask  bool              `json:"is_zeebe_user_task,omitempty"`
}

type BoundaryTimer struct {
	Duration     string `json:"duration"`
	Interrupting bool   `json:"interrupting"`
	TargetDept   string `json:"target_dept,omitempty"`
}

type ExecutionPlan struct {
	Steps []ExecutionStep `json:"steps"`
}

type ExecutionStep struct {
	Sequential   []string          `json:"sequential,omitempty"`
	Parallel     []ParallelBranch  `json:"parallel,omitempty"`
	Exclusive    []ExclusiveBranch `json:"exclusive,omitempty"`
	SubWorkflow  *SubWorkflowStep  `json:"sub_workflow,omitempty"`
	CallPool     *CallPoolStep     `json:"call_pool,omitempty"`
	IOMapping    *IOMapping        `json:"io_mapping,omitempty"`
	Extras       map[string]string `json:"extras,omitempty"`
	MessagePaths []MessagePath     `json:"message_paths,omitempty"`
}

// MessagePath represents a message boundary event attached to a callActivity,
// subProcess, or plain userTask step. The execution service uses MessageName
// and Interrupting to route at runtime.
type MessagePath struct {
	MessageName  string `json:"message_name"`
	Interrupting bool   `json:"interrupting"`
	TargetDept   string `json:"target_dept,omitempty"`
}

// IOMapping carries variable input/output declarations for a callActivity step.
// The Execution Service uses this to pass variables when creating child workflows.
type IOMapping struct {
	Inputs  []IOVar `json:"inputs,omitempty"`
	Outputs []IOVar `json:"outputs,omitempty"`
}

type IOVar struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

// ParallelBranch is one branch of a parallel gateway, containing its own
// sequential execution steps rather than a bare department ID.
type ParallelBranch struct {
	DeptID string          `json:"dept"`
	Steps  []ExecutionStep `json:"steps"`
}

// CallPoolStep represents a step where the main pool hands control to another
// compiled pool (Temporal child workflow). Only the main pool may emit this.
type CallPoolStep struct {
	Pool string `json:"pool"`
}

type ExclusiveBranch struct {
	Target      string `json:"target,omitempty"`
	TargetStage string `json:"target_stage,omitempty"`
	// TargetNodeID is the BPMN element ID of the first user/send/receive task or
	// subprocess on this branch — used for stable machine-addressable routing.
	TargetNodeID string `json:"target_node_id,omitempty"`
	// TargetName is the BPMN element name of the target; kept for human reference.
	TargetName          string `json:"target_name,omitempty"`
	ConditionExpression string `json:"condition_expression"`
	// Terminates is true when this branch leads directly to an end event.
	Terminates bool `json:"terminates,omitempty"`
	// RevertToDept / RevertToStage / RevertToNodeID / RevertToName are set when
	// this branch is a back-edge (guarded revert/loop flow) instead of a forward branch.
	RevertToDept   string `json:"revert_to_dept,omitempty"`
	RevertToStage  string `json:"revert_to_stage,omitempty"`
	RevertToNodeID string `json:"revert_to_node_id,omitempty"`
	RevertToName   string `json:"revert_to_name,omitempty"`
}

type SubWorkflowStep struct {
	NodeID       string        `json:"node_id,omitempty"`
	Name         string        `json:"name"`
	Plan         ExecutionPlan `json:"plan"`
	ErrorPaths   []ErrorPath   `json:"error_paths,omitempty"`
	TimerPaths   []TimerPath   `json:"timer_paths,omitempty"`
	MessagePaths []MessagePath `json:"message_paths,omitempty"`
}

type ErrorPath struct {
	ErrorCode    string `json:"error_code,omitempty"`
	ShortCircuit bool   `json:"short_circuit"`
	TargetDept   string `json:"target_dept,omitempty"`
}

type TimerPath struct {
	Duration     string `json:"duration"`
	Interrupting bool   `json:"interrupting"`
	TargetDept   string `json:"target_dept,omitempty"`
}

// CompiledCollaboration wraps multiple compiled plans for multi-participant BPMN.
// MainPlan is the name of the primary pool (Temporal workflow entry point).
// All pools — including those marked ignored — appear in Plans.
type CompiledCollaboration struct {
	MainPlan string          `json:"main_plan"`
	Plans    []*CompiledPlan `json:"plans"`
	Messages []MessageDef    `json:"messages"`
}

// MessageDef links two plans via a named BPMN message.
type MessageDef struct {
	Name       string `json:"name"`
	SourcePlan string `json:"source_plan"` // process name of the sending participant
	TargetPlan string `json:"target_plan"` // process name of the receiving participant
}
