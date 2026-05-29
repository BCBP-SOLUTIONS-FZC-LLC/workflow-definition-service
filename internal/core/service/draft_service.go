package service

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type DraftDeps struct {
	Workflows port.WorkflowRepository
	Versions  port.WorkflowVersionRepository
	Assignees port.AssigneeRepository
	Compiler  port.PlanCompiler
	Log       port.Logger
}

type DraftService struct {
	workflows port.WorkflowRepository
	versions  port.WorkflowVersionRepository
	assignees port.AssigneeRepository
	compiler  port.PlanCompiler
	log       port.Logger
}

func NewDraftService(d DraftDeps) *DraftService {
	return &DraftService{
		workflows: d.Workflows,
		versions:  d.Versions,
		assignees: d.Assignees,
		compiler:  d.Compiler,
		log:       d.Log,
	}
}

// UpdateDraftReq carries optional fields that may be patched on the draft.
type UpdateDraftReq struct {
	Name        *string
	Description *string
	BPMNXML     *string
}

func (s *DraftService) Get(_ context.Context, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
	return nil, errors.New("not implemented")
}

// Init creates a new DRAFT by copying the active published version's BPMN XML.
func (s *DraftService) Init(_ context.Context, _, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
	return nil, errors.New("not implemented")
}

// Update saves canvas changes to the active draft (optimistic lock via updated_at).
func (s *DraftService) Update(_ context.Context, _, _, _ uuid.UUID, _ UpdateDraftReq) (*domain.WorkflowVersion, error) {
	return nil, errors.New("not implemented")
}

func (s *DraftService) Discard(_ context.Context, _, _ uuid.UUID) error {
	return errors.New("not implemented")
}
