package domain

type CompiledPlan struct {
	Name        string          `json:"name"`
	TaskQueue   string          `json:"task_queue"`
	Departments []DepartmentDef `json:"departments"`
	Execution   ExecutionPlan   `json:"execution"`
}

type DepartmentDef struct {
	ID     string     `json:"id"`
	Label  string     `json:"label"`
	Stages []StageDef `json:"stages"`
}

type StageDef struct {
	Type             string         `json:"type"`
	Activity         string         `json:"activity"`
	Role             string         `json:"role"`
	DefaultAssignees []string       `json:"default_assignees,omitempty"`
	RequiresComment  bool           `json:"requires_comment"`
	BoundaryTimer    *BoundaryTimer `json:"boundary_timer,omitempty"`
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
	Sequential  []string          `json:"sequential,omitempty"`
	Parallel    []string          `json:"parallel,omitempty"`
	Exclusive   []ExclusiveBranch `json:"exclusive,omitempty"`
	SubWorkflow *SubWorkflowStep  `json:"sub_workflow,omitempty"`
}

type ExclusiveBranch struct {
	Target              string `json:"target"`
	ConditionExpression string `json:"condition_expression"`
	// RevertToDept / RevertToStage are set when this branch is a back-edge
	// (a guarded revert/loop flow) instead of a forward branch.
	RevertToDept  string `json:"revert_to_dept,omitempty"`
	RevertToStage string `json:"revert_to_stage,omitempty"`
}

type SubWorkflowStep struct {
	Name       string        `json:"name"`
	Plan       ExecutionPlan `json:"plan"`
	ErrorPaths []ErrorPath   `json:"error_paths,omitempty"`
	TimerPaths []TimerPath   `json:"timer_paths,omitempty"`
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
type CompiledCollaboration struct {
	Plans    []*CompiledPlan `json:"plans"`
	Messages []MessageDef    `json:"messages"`
}

// MessageDef links two plans via a named BPMN message.
type MessageDef struct {
	Name       string `json:"name"`
	SourcePlan string `json:"source_plan"` // process ID of the sending participant
	TargetPlan string `json:"target_plan"` // process ID of the receiving participant
}
