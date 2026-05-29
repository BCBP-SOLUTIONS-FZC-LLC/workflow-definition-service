package service

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type WorkflowDeps struct {
	Workflows port.WorkflowRepository
	Versions  port.WorkflowVersionRepository
	Outbox    port.OutboxRepository
	Cache     port.CacheStore
	Execution port.ExecutionService
	Log       port.Logger
}

type WorkflowService struct {
	workflows port.WorkflowRepository
	versions  port.WorkflowVersionRepository
	outbox    port.OutboxRepository
	cache     port.CacheStore
	execution port.ExecutionService
	log       port.Logger
}

func NewWorkflowService(d WorkflowDeps) *WorkflowService {
	return &WorkflowService{
		workflows: d.Workflows,
		versions:  d.Versions,
		outbox:    d.Outbox,
		cache:     d.Cache,
		execution: d.Execution,
		log:       d.Log,
	}
}

func (s *WorkflowService) List(_ context.Context, _ uuid.UUID, _ port.WorkflowFilter) ([]*domain.Workflow, int64, error) {
	return nil, 0, errors.New("not implemented")
}

// Create creates the workflow root record and the initial DRAFT version in one transaction.
func (s *WorkflowService) Create(_ context.Context, _, _ uuid.UUID, _, _, _, _ string) (*domain.Workflow, *domain.WorkflowVersion, error) {
	return nil, nil, errors.New("not implemented")
}

// Get returns the workflow root along with a page of its versions.
func (s *WorkflowService) Get(_ context.Context, _, _ uuid.UUID) (*domain.Workflow, []*domain.WorkflowVersion, error) {
	return nil, nil, errors.New("not implemented")
}

// Archive deactivates the workflow after confirming no active instances exist.
func (s *WorkflowService) Archive(_ context.Context, _, _, _ uuid.UUID) error {
	return errors.New("not implemented")
}
