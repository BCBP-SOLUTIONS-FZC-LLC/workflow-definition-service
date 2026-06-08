package domain

const (
	EventTypeTemplatePublished              = "workflow.template.published"
	EventTypeTemplateArchived               = "workflow.template.archived"
	EventTypeTemplateEligibilityInvalidated = "workflow.template.eligibility_invalidated"

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
	WorkflowID         string `json:"workflow_id"`
	VersionID          string `json:"version_id"`
	AffectedUserID     string `json:"affected_user_id"`
	AffectedDepartment string `json:"affected_department"`
}
