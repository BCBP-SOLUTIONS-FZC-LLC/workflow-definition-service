package domain

const (
	EventTypeTemplatePublished = "workflow.template.published"

	EventSource = "workflow-definition-svc"
)

type TemplatePublishedPayload struct {
	WorkflowID            string  `json:"workflow_id"`
	WorkflowKey           string  `json:"workflow_key"`
	VersionID             string  `json:"version_id"`
	VersionNumber         int32   `json:"version_number"`
	ArtifactHash          string  `json:"artifact_hash"`
	PublishedBy           string  `json:"published_by"`
	PromotedFromVersionID *string `json:"promoted_from_version_id,omitempty"`
}
