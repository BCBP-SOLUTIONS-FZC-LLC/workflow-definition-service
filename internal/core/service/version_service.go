package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

const (
	errGetVersion  = "get version: %w"
	errGetWorkflow = "get workflow: %w"
)

type VersionDeps struct {
	Transactor port.Transactor
	Workflows  port.WorkflowRepository
	Versions   port.WorkflowVersionRepository
	Assignees  port.AssigneeRepository
	Outbox     port.OutboxRepository
	Membership port.MembershipService
	Execution  port.ExecutionService
	Compiler   port.PlanCompiler
	Cache      port.CacheStore
	Log        port.Logger
}

type VersionService struct {
	transactor port.Transactor
	workflows  port.WorkflowRepository
	versions   port.WorkflowVersionRepository
	assignees  port.AssigneeRepository
	outbox     port.OutboxRepository
	membership port.MembershipService
	execution  port.ExecutionService
	compiler   port.PlanCompiler
	cache      port.CacheStore
	log        port.Logger
}

func NewVersionService(d VersionDeps) *VersionService {
	return &VersionService{
		transactor: d.Transactor,
		workflows:  d.Workflows,
		versions:   d.Versions,
		assignees:  d.Assignees,
		outbox:     d.Outbox,
		membership: d.Membership,
		execution:  d.Execution,
		compiler:   d.Compiler,
		cache:      d.Cache,
		log:        logOrNoop(d.Log),
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
	forcePublishStructural bool,
) (v *domain.WorkflowVersion, err error) {
	start := time.Now()
	defer func() {
		observability.IncCounterVec(observability.WFPublishTotal, observability.OutcomeLabel(err))
		observability.ObserveHistogram(observability.WFPublishLatencySeconds, time.Since(start).Seconds())
	}()
	draft, compiledJSON, artifactHash, assignees, err :=
		s.publishPreFlight(ctx, tenantID, workflowID, versionID, forcePublishStructural)
	if err != nil {
		return nil, err
	}

	var versionNumber int32
	if err := s.transactor.RunInTxWithRetry(ctx, func(ctx context.Context) error {
		n, err := s.versions.NextVersionNumber(ctx, tenantID, workflowID)
		if err != nil {
			return fmt.Errorf("next version number: %w", err)
		}
		versionNumber = n
		return s.runPublishTx(ctx, publishTxInput{
			tenantID:      tenantID,
			workflowID:    workflowID,
			versionID:     versionID,
			versionNumber: versionNumber,
			compiledJSON:  compiledJSON,
			artifactHash:  artifactHash,
			assignees:     assignees,
		})
	}); err != nil {
		return nil, fmt.Errorf("publish version: %w", err)
	}

	draft.Status = domain.VersionStatusPublished
	draft.VersionNumber = &versionNumber
	draft.CompiledPlanJSON = &compiledJSON
	draft.ArtifactHash = artifactHash
	s.log.Info("version published", map[string]any{
		"tenant_id":      tenantID.String(),
		"user_id":        userID.String(),
		"workflow_id":    workflowID.String(),
		"version_id":     versionID.String(),
		"version_number": versionNumber,
	})
	return draft, nil
}

func (s *VersionService) Clone(
	ctx context.Context,
	tenantID, userID, workflowID, versionID uuid.UUID,
	planTier string,
	req CloneReq,
) (wf *domain.Workflow, v *domain.WorkflowVersion, err error) {
	defer func() { observability.IncCounterVec(observability.WFCloneTotal, observability.OutcomeLabel(err)) }()
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

	newWFID, err := uuid.NewV7()
	if err != nil {
		return nil, nil, fmt.Errorf("generate workflow id: %w", err)
	}
	newVersionID, err := uuid.NewV7()
	if err != nil {
		return nil, nil, fmt.Errorf("generate version id: %w", err)
	}
	newWF := &domain.Workflow{
		ID:              newWFID,
		TenantID:        tenantID,
		CreatedByUserID: userID,
		BusinessKey:     req.NewKey,
		Name:            req.NewName,
		Description:     req.NewDescription,
	}
	newVersion := &domain.WorkflowVersion{
		ID:              newVersionID,
		WorkflowID:      newWF.ID,
		TenantID:        tenantID,
		Status:          domain.VersionStatusDraft,
		BPMNXML:         source.BPMNXML,
		ModuleBPMNXMLs:  source.ModuleBPMNXMLs,
		CreatedByUserID: userID,
		IsValid:         true,
	}
	// Quota COUNT + inserts in one SERIALIZABLE tx so concurrent clones can't both
	// pass the quota check (LLD §5.6.2).
	if err := s.transactor.RunInTxWithRetry(ctx, func(ctx context.Context) error {
		if err := enforceWorkflowQuota(ctx, s.workflows, tenantID, planTier); err != nil {
			return err
		}
		if err := s.workflows.Create(ctx, newWF); err != nil {
			return err
		}
		return s.versions.Create(ctx, newVersion)
	}); err != nil {
		return nil, nil, fmt.Errorf("clone version: %w", err)
	}
	s.log.Info("version cloned", map[string]any{
		"tenant_id":          tenantID.String(),
		"user_id":            userID.String(),
		"source_workflow_id": workflowID.String(),
		"source_version_id":  versionID.String(),
		"new_workflow_id":    newWF.ID.String(),
		"new_version_id":     newVersion.ID.String(),
	})
	return newWF, newVersion, nil
}

func (s *VersionService) Promote(
	ctx context.Context,
	tenantID, userID, workflowID, versionID uuid.UUID,
) (v *domain.WorkflowVersion, err error) {
	defer func() { observability.IncCounterVec(observability.WFPromoteTotal, observability.OutcomeLabel(err)) }()
	v, err = s.versions.GetByID(ctx, tenantID, versionID)
	if err != nil {
		return nil, fmt.Errorf(errGetVersion, err)
	}
	if v.WorkflowID != workflowID {
		return nil, domain.ErrNotFound
	}
	if v.Status != domain.VersionStatusPublished {
		return nil, domain.ErrVersionNotPublished
	}

	wf, err := s.workflows.GetByID(ctx, tenantID, workflowID)
	if err != nil {
		return nil, fmt.Errorf(errGetWorkflow, err)
	}
	if wf.ActiveVersionID != nil && *wf.ActiveVersionID == versionID {
		return v, nil
	}

	if err := s.transactor.RunInTx(ctx, func(ctx context.Context) error {
		return s.workflows.UpdateActiveVersion(ctx, tenantID, workflowID, &versionID)
	}); err != nil {
		return nil, fmt.Errorf("promote version: %w", err)
	}

	s.log.Info("version promoted", map[string]any{
		"tenant_id":   tenantID.String(),
		"user_id":     userID.String(),
		"workflow_id": workflowID.String(),
		"version_id":  versionID.String(),
	})
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
		return "", "", fmt.Errorf(errGetWorkflow, err)
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
	changeType := ChangeTypeStructural
	if changes.MetadataOnly {
		changeType = ChangeTypeMetadataOnly
	}
	return &DiffResult{
		WorkflowID:      workflowID,
		BaseVersionID:   baseVersionID,
		TargetVersionID: targetVersionID,
		ChangeType:      changeType,
		Changes:         changes,
	}, nil
}

func (s *VersionService) resolvePlan(
	ctx context.Context,
	v *domain.WorkflowVersion,
) (*dsl.CompiledPlan, error) {
	if v.CompiledPlanJSON != nil && *v.CompiledPlanJSON != "" {
		var plan dsl.CompiledPlan
		if err := json.Unmarshal([]byte(*v.CompiledPlanJSON), &plan); err == nil {
			return &plan, nil
		}
	}
	bundled, err := s.compiler.Bundle(v.BPMNXML, v.ModuleBPMNXMLs)
	if err != nil {
		return nil, fmt.Errorf("bundle modules: %w", err)
	}
	compileStart := time.Now()
	plan, err := s.compiler.Compile(ctx, bundled)
	observability.ObserveHistogram(observability.WFCompileDurationSeconds, time.Since(compileStart).Seconds())
	return plan, err
}

func versionLabel(v *domain.WorkflowVersion) string {
	if v.VersionNumber != nil {
		return fmt.Sprintf("%d", *v.VersionNumber)
	}
	return "draft"
}
