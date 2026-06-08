package domain

const (
	EventTypeTemplatePublished              = "workflow.template.published"
	EventTypeTemplateArchived               = "workflow.template.archived"
	EventTypeTemplateEligibilityInvalidated = "workflow.template.eligibility_invalidated"
	EventTypeTemplateCloned                 = "workflow.template.cloned"

	EventSource = "workflow-definition-svc"
)

type TemplatePublishedPayload struct {
	WorkflowID       string `json:"workflow_id"`
	VersionID        string `json:"version_id"`
	VersionNumber    int32  `json:"version_number"`
	PublishedBy      string `json:"published_by"`
	CompiledPlanJSON string `json:"compiled_plan_json"`
}

type TemplateArchivedPayload struct {
	WorkflowID string `json:"workflow_id"`
	VersionID  string `json:"version_id"`
	ArchivedBy string `json:"archived_by"`
}

type TemplateEligibilityInvalidatedPayload struct {
	WorkflowID    string   `json:"workflow_id"`
	VersionID     string   `json:"version_id"`
	VersionNumber int32    `json:"version_number"`
	RevokedUserID string   `json:"revoked_user_id"`
	AffectedNodes []string `json:"affected_nodes"`
	Reason        string   `json:"reason"`
}

type TemplateClonedPayload struct {
	SourceWorkflowID string `json:"source_workflow_id"`
	SourceVersionID  string `json:"source_version_id"`
	NewWorkflowID    string `json:"new_workflow_id"`
	NewWorkflowKey   string `json:"new_workflow_key"`
	NewWorkflowName  string `json:"new_workflow_name"`
	ClonedByUserID   string `json:"cloned_by_user_id"`
}
