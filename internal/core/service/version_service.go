package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

const errGetVersion = "get version: %w"

type VersionDeps struct {
	Transactor      port.Transactor
	Workflows       port.WorkflowRepository
	Versions        port.WorkflowVersionRepository
	Assignees       port.AssigneeRepository
	Outbox          port.OutboxRepository
	ProcessedEvents port.ProcessedEventRepository
	Membership      port.MembershipService
	Execution       port.ExecutionService
	Compiler        port.PlanCompiler
	Log             port.Logger
}

type VersionService struct {
	transactor      port.Transactor
	workflows       port.WorkflowRepository
	versions        port.WorkflowVersionRepository
	assignees       port.AssigneeRepository
	outbox          port.OutboxRepository
	processedEvents port.ProcessedEventRepository
	membership      port.MembershipService
	execution       port.ExecutionService
	compiler        port.PlanCompiler
	log             port.Logger
}

func NewVersionService(d VersionDeps) *VersionService {
	return &VersionService{
		transactor:      d.Transactor,
		workflows:       d.Workflows,
		versions:        d.Versions,
		assignees:       d.Assignees,
		outbox:          d.Outbox,
		processedEvents: d.ProcessedEvents,
		membership:      d.Membership,
		execution:       d.Execution,
		compiler:        d.Compiler,
		log:             d.Log,
	}
}

func (s *VersionService) List(
	ctx context.Context,
	tenantID, workflowID uuid.UUID,
	page, limit int,
) ([]*domain.WorkflowVersion, int64, error) {
	versions, count, err := s.versions.ListByWorkflow(ctx, tenantID, workflowID, page, limit)
	if err != nil {
		return nil, 0, fmt.Errorf("list versions: %w", err)
	}
	return versions, count, nil
}

func (s *VersionService) Get(
	ctx context.Context,
	tenantID, workflowID, versionID uuid.UUID,
) (*domain.WorkflowVersion, error) {
	v, err := s.versions.GetByID(ctx, tenantID, versionID)
	if err != nil {
		return nil, fmt.Errorf(errGetVersion, err)
	}
	if v.WorkflowID != workflowID {
		return nil, domain.ErrNotFound
	}
	return v, nil
}

func (s *VersionService) Publish(
	ctx context.Context,
	tenantID, userID, workflowID, versionID uuid.UUID,
	skipEligibilityCheck bool,
) (*domain.WorkflowVersion, error) {
	draft, compiledJSON, artifactHash, versionNumber, assignees, err :=
		s.publishPreFlight(ctx, tenantID, workflowID, versionID, skipEligibilityCheck)
	if err != nil {
		return nil, err
	}

	env, err := buildEnvelope(domain.EventTypeTemplatePublished, tenantID.String(), domain.TemplatePublishedPayload{
		WorkflowID:       workflowID.String(),
		VersionID:        versionID.String(),
		VersionNumber:    versionNumber,
		PublishedBy:      userID.String(),
		CompiledPlanJSON: compiledJSON,
	})
	if err != nil {
		return nil, fmt.Errorf("build publish event: %w", err)
	}

	txIn := publishTxInput{
		tenantID:      tenantID,
		workflowID:    workflowID,
		versionID:     versionID,
		versionNumber: versionNumber,
		compiledJSON:  compiledJSON,
		artifactHash:  artifactHash,
		assignees:     assignees,
		env:           env,
	}
	if err := s.transactor.RunInTx(ctx, func(ctx context.Context) error {
		return s.runPublishTx(ctx, txIn)
	}); err != nil {
		return nil, fmt.Errorf("publish version: %w", err)
	}

	draft.Status = domain.VersionStatusPublished
	draft.VersionNumber = &versionNumber
	draft.CompiledPlanJSON = &compiledJSON
	draft.ArtifactHash = artifactHash
	return draft, nil
}

func (s *VersionService) Clone(
	ctx context.Context,
	tenantID, userID, workflowID, versionID uuid.UUID,
	req CloneReq,
) (*domain.Workflow, *domain.WorkflowVersion, error) {
	source, err := s.versions.GetByID(ctx, tenantID, versionID)
	if err != nil {
		return nil, nil, fmt.Errorf("get source version: %w", err)
	}
	if source.WorkflowID != workflowID {
		return nil, nil, domain.ErrNotFound
	}
	if source.Status == domain.VersionStatusDraft {
		return nil, nil, domain.ErrVersionNotPublished
	}

	newWF := &domain.Workflow{
		ID:              uuid.New(),
		TenantID:        tenantID,
		CreatedByUserID: userID,
		BusinessKey:     req.NewKey,
		Name:            req.NewName,
		Description:     req.NewDescription,
	}
	newVersion := &domain.WorkflowVersion{
		ID:              uuid.New(),
		WorkflowID:      newWF.ID,
		TenantID:        tenantID,
		Status:          domain.VersionStatusDraft,
		BPMNXML:         source.BPMNXML,
		CreatedByUserID: userID,
		IsValid:         true,
	}
	env, err := buildEnvelope(domain.EventTypeTemplateCloned, tenantID.String(), domain.TemplateClonedPayload{
		SourceWorkflowID: workflowID.String(),
		SourceVersionID:  versionID.String(),
		NewWorkflowID:    newWF.ID.String(),
		NewWorkflowKey:   req.NewKey,
		NewWorkflowName:  req.NewName,
		ClonedByUserID:   userID.String(),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("build clone event: %w", err)
	}

	if err := s.transactor.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.workflows.Create(ctx, newWF); err != nil {
			return err
		}
		if err := s.versions.Create(ctx, newVersion); err != nil {
			return err
		}
		return s.outbox.Enqueue(ctx, env)
	}); err != nil {
		return nil, nil, fmt.Errorf("clone version: %w", err)
	}
	return newWF, newVersion, nil
}

func (s *VersionService) Promote(
	ctx context.Context,
	tenantID, userID, workflowID, versionID uuid.UUID,
) (*domain.WorkflowVersion, error) {
	v, err := s.versions.GetByID(ctx, tenantID, versionID)
	if err != nil {
		return nil, fmt.Errorf(errGetVersion, err)
	}
	if v.WorkflowID != workflowID {
		return nil, domain.ErrNotFound
	}
	if v.Status != domain.VersionStatusPublished {
		return nil, domain.ErrVersionNotPublished
	}
	if err := s.workflows.UpdateActiveVersion(ctx, tenantID, workflowID, &versionID); err != nil {
		return nil, fmt.Errorf("promote version: %w", err)
	}
	return v, nil
}

func (s *VersionService) Export(
	ctx context.Context,
	tenantID, workflowID, versionID uuid.UUID,
) (bpmnXML string, filename string, err error) {
	v, err := s.versions.GetByID(ctx, tenantID, versionID)
	if err != nil {
		return "", "", fmt.Errorf(errGetVersion, err)
	}
	if v.WorkflowID != workflowID {
		return "", "", domain.ErrNotFound
	}
	wf, err := s.workflows.GetByID(ctx, tenantID, workflowID)
	if err != nil {
		return "", "", fmt.Errorf("get workflow: %w", err)
	}
	return v.BPMNXML, fmt.Sprintf("%s-v%s.bpmn", wf.BusinessKey, versionLabel(v)), nil
}

func (s *VersionService) Diff(
	ctx context.Context,
	tenantID, workflowID, baseVersionID, targetVersionID uuid.UUID,
) (*DiffResult, error) {
	base, err := s.versions.GetByID(ctx, tenantID, baseVersionID)
	if err != nil {
		return nil, fmt.Errorf("get base version: %w", err)
	}
	target, err := s.versions.GetByID(ctx, tenantID, targetVersionID)
	if err != nil {
		return nil, fmt.Errorf("get target version: %w", err)
	}
	if base.WorkflowID != workflowID || target.WorkflowID != workflowID {
		return nil, domain.ErrNotFound
	}

	basePlan, err := s.resolvePlan(ctx, base)
	if err != nil {
		return nil, fmt.Errorf("resolve base plan: %w", err)
	}
	targetPlan, err := s.resolvePlan(ctx, target)
	if err != nil {
		return nil, fmt.Errorf("resolve target plan: %w", err)
	}

	changes := computeDiff(basePlan, targetPlan)
	changeType := "STRUCTURAL"
	if changes.MetadataOnly {
		changeType = "METADATA_ONLY"
	}
	return &DiffResult{
		WorkflowID:      workflowID,
		BaseVersionID:   baseVersionID,
		TargetVersionID: targetVersionID,
		ChangeType:      changeType,
		Changes:         changes,
	}, nil
}

// resolvePlan returns the compiled plan for a version, using the stored
// compiled_plan_json when available to avoid redundant recompilation.
func (s *VersionService) resolvePlan(
	ctx context.Context,
	v *domain.WorkflowVersion,
) (*domain.CompiledPlan, error) {
	if v.CompiledPlanJSON != nil && *v.CompiledPlanJSON != "" {
		var plan domain.CompiledPlan
		if err := json.Unmarshal([]byte(*v.CompiledPlanJSON), &plan); err == nil {
			return &plan, nil
		}
	}
	return s.compiler.Compile(ctx, v.BPMNXML)
}

func versionLabel(v *domain.WorkflowVersion) string {
	if v.VersionNumber != nil {
		return fmt.Sprintf("%d", *v.VersionNumber)
	}
	return "draft"
}
