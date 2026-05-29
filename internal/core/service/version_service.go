package service

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type VersionDeps struct {
	Workflows  port.WorkflowRepository
	Versions   port.WorkflowVersionRepository
	Assignees  port.AssigneeRepository
	Outbox     port.OutboxRepository
	Membership port.MembershipService
	Execution  port.ExecutionService
	Compiler   port.PlanCompiler
	Log        port.Logger
}

type VersionService struct {
	workflows  port.WorkflowRepository
	versions   port.WorkflowVersionRepository
	assignees  port.AssigneeRepository
	outbox     port.OutboxRepository
	membership port.MembershipService
	execution  port.ExecutionService
	compiler   port.PlanCompiler
	log        port.Logger
}

func NewVersionService(d VersionDeps) *VersionService {
	return &VersionService{
		workflows:  d.Workflows,
		versions:   d.Versions,
		assignees:  d.Assignees,
		outbox:     d.Outbox,
		membership: d.Membership,
		execution:  d.Execution,
		compiler:   d.Compiler,
		log:        d.Log,
	}
}

// DiffResult is the structured output of a two-version structural comparison.
type DiffResult struct {
	WorkflowID      uuid.UUID
	BaseVersionID   uuid.UUID
	TargetVersionID uuid.UUID
	ChangeType      string // STRUCTURAL | METADATA_ONLY
	Changes         DiffChanges
}

type DiffChanges struct {
	AddedDepartments   []string
	RemovedDepartments []string
	StepChanges        []StepChange
	MetadataOnly       bool
}

type StepChange struct {
	Description string
	Before      *domain.ExecutionStep
	After       *domain.ExecutionStep
}

func (s *VersionService) List(_ context.Context, _, _ uuid.UUID, _, _ int) ([]*domain.WorkflowVersion, int64, error) {
	return nil, 0, errors.New("not implemented")
}

func (s *VersionService) Get(_ context.Context, _, _, _ uuid.UUID) (*domain.WorkflowVersion, error) {
	return nil, errors.New("not implemented")
}

// Publish validates, compiles, and transitions the draft to PUBLISHED inside a serializable transaction.
func (s *VersionService) Publish(_ context.Context, _, _, _, _ uuid.UUID, _ bool) (*domain.WorkflowVersion, error) {
	return nil, errors.New("not implemented")
}

// Clone copies a PUBLISHED or ARCHIVED version into a new workflow under newKey.
func (s *VersionService) Clone(_ context.Context, _, _, _, _ uuid.UUID, _, _, _ string) (*domain.Workflow, *domain.WorkflowVersion, error) {
	return nil, nil, errors.New("not implemented")
}

// Promote sets a PUBLISHED version as the workflow's active version (rollback / rollforward).
func (s *VersionService) Promote(_ context.Context, _, _, _, _ uuid.UUID) error {
	return errors.New("not implemented")
}

// Export returns the raw BPMN XML and a suggested download filename for the version.
func (s *VersionService) Export(_ context.Context, _, _, _ uuid.UUID) (bpmnXML string, filename string, err error) {
	return "", "", errors.New("not implemented")
}

func (s *VersionService) Diff(_ context.Context, _, _, _, _ uuid.UUID) (*DiffResult, error) {
	return nil, errors.New("not implemented")
}
