package handler

import (
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

// Handler aggregates all request-handling methods grouped by OpenAPI tag.
// Each method group delegates to its matching service.
type Handler struct {
	workflows  *service.WorkflowService
	drafts     *service.DraftService
	versions   *service.VersionService
	validation *service.ValidationService
}

type Services struct {
	Workflows  *service.WorkflowService
	Drafts     *service.DraftService
	Versions   *service.VersionService
	Validation *service.ValidationService
}

func New(s Services) *Handler {
	return &Handler{
		workflows:  s.Workflows,
		drafts:     s.Drafts,
		versions:   s.Versions,
		validation: s.Validation,
	}
}
