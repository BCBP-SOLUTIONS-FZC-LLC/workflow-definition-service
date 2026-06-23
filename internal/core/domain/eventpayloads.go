package domain

const (
	EventTypeTemplatePublished = "workflow.template.published"

	EventSource = "workflow-definition-svc"
)

type TemplatePublishedPayload struct {
	WorkflowID            string  `json:"workflow_id"`
	VersionID             string  `json:"version_id"`
	VersionNumber         int32   `json:"version_number"`
	PublishedBy           string  `json:"published_by"`
	CompiledPlanJSON      string  `json:"compiled_plan_json"`
	PromotedFromVersionID *string `json:"promoted_from_version_id,omitempty"`
}
