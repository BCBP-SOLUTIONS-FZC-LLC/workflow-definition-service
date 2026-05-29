package domain

type CompiledPlan struct {
	Name        string           `json:"name"`
	Version     string           `json:"version"`
	TaskQueue   string           `json:"task_queue"`
	Departments []DepartmentDef  `json:"departments"`
	Execution   ExecutionPlan    `json:"execution"`
}

type DepartmentDef struct {
	ID     string     `json:"id"`
	Label  string     `json:"label"`
	Stages []StageDef `json:"stages"`
}

type StageDef struct {
	Type                string   `json:"type"`
	Activity            string   `json:"activity"`
	Role                string   `json:"role"`
	DefaultAssignees      []string `json:"default_assignees,omitempty"`
	AssigneeMode        string   `json:"assignee_mode,omitempty"`
	RequiresComment     bool     `json:"requires_comment"`
	SLADuration         *string  `json:"sla_duration,omitempty"`
	ConditionExpression *string  `json:"condition_expression,omitempty"`
}

type ExecutionPlan struct {
	Steps []ExecutionStep `json:"steps"`
}

type ExecutionStep struct {
	Sequential []string          `json:"sequential,omitempty"`
	Parallel   []string          `json:"parallel,omitempty"`
	Exclusive  []ExclusiveBranch `json:"exclusive,omitempty"`
}

type ExclusiveBranch struct {
	Target              string `json:"target"`
	ConditionExpression string `json:"condition_expression"`
}
