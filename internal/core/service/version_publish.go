package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

type publishTxInput struct {
	tenantID      uuid.UUID
	workflowID    uuid.UUID
	versionID     uuid.UUID
	recordVersion int64
	versionNumber int32
	compiledJSON  string
	artifactHash  string
	assignees     []*domain.NodeAssignee
	moduleRefs    []domain.ModuleRef
}

type publishPrep struct {
	compiledJSON string
	artifactHash string
	assignees    []*domain.NodeAssignee
	moduleRefs   []domain.ModuleRef
}

func (s *VersionService) publishPreFlight(
	ctx context.Context,
	tenantID, workflowID, versionID uuid.UUID,
	forcePublishStructural bool,
) (*domain.WorkflowVersion, *publishPrep, error) {
	draft, err := s.versions.GetByID(ctx, tenantID, versionID)
	if err != nil {
		return nil, nil, fmt.Errorf("get draft version: %w", err)
	}
	if draft.Status != domain.VersionStatusDraft {
		return nil, nil, domain.ErrVersionNotDraft
	}
	if draft.WorkflowID != workflowID {
		return nil, nil, domain.ErrNotFound
	}

	res, err := s.modules.resolve(ctx, s.modules.live(tenantID), draft.BPMNXML, "")
	if err != nil {
		return nil, nil, err
	}
	if len(res.issues) > 0 {
		return nil, nil, &domain.ValidationFailedError{Errors: res.issues}
	}
	collab, plan, err := s.compileForPublish(ctx, res.bundled)
	if err != nil {
		return nil, nil, err
	}
	artifactHash, err := s.compiler.Hash(ctx, res.bundled)
	if err != nil {
		return nil, nil, fmt.Errorf("hash bpmn: %w", err)
	}

	if s.workflows != nil {
		wf, err := s.workflows.GetByID(ctx, tenantID, workflowID)
		if err != nil {
			return nil, nil, fmt.Errorf(errGetWorkflow, err)
		}
		if err := s.checkStructuralDivergence(ctx, tenantID, wf, plan, forcePublishStructural); err != nil {
			return nil, nil, err
		}
	}

	if s.membership != nil {
		if err := s.checkAssigneeEligibility(ctx, tenantID, plan); err != nil {
			return nil, nil, err
		}
	}

	compiledJSON, err := marshalPlan(collab)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal compiled plan: %w", err)
	}

	return draft, &publishPrep{
		compiledJSON: compiledJSON,
		artifactHash: artifactHash,
		assignees:    extractAssignees(tenantID, versionID, plan),
		moduleRefs:   res.refs,
	}, nil
}

func (s *VersionService) compileForPublish(
	ctx context.Context,
	bundledXML string,
) (*dsl.CompiledCollaboration, *dsl.CompiledPlan, error) {
	compileStart := time.Now()
	ctx, endSpan := observability.StartSpan(ctx, "BPMN_Compilation")
	collab, err := s.compiler.CompileForPublish(ctx, bundledXML)
	endSpan()
	observability.ObserveHistogram(observability.WFCompileDurationSeconds, time.Since(compileStart).Seconds())
	if err != nil {
		return nil, nil, fmt.Errorf("compile bpmn: %w", err)
	}
	plan, err := expandedMainPlan(collab)
	if err != nil {
		return nil, nil, fmt.Errorf("compile bpmn: %w", err)
	}
	return collab, plan, nil
}

func (s *VersionService) checkStructuralDivergence(
	ctx context.Context,
	tenantID uuid.UUID,
	wf *domain.Workflow,
	draftPlan *dsl.CompiledPlan,
	forcePublishStructural bool,
) error {
	if wf.ActiveVersionID == nil {
		return nil
	}

	active, err := s.versions.GetByID(ctx, tenantID, *wf.ActiveVersionID)
	if err != nil {
		return fmt.Errorf("get active version for divergence check: %w", err)
	}

	activePlan, err := s.resolvePlan(ctx, active)
	var invalid *domain.ValidationFailedError
	switch {
	case errors.As(err, &invalid) || errors.Is(err, domain.ErrMalformedBPMN):
		if forcePublishStructural {
			return nil
		}
		return fmt.Errorf("active version no longer compiles: %w", domain.ErrStructuralDivergence)
	case err != nil:
		return fmt.Errorf("compile active version for divergence check: %w", err)
	}

	diff := computeDiff(activePlan, draftPlan)
	if !diff.MetadataOnly && !forcePublishStructural {
		return domain.ErrStructuralDivergence
	}
	return nil
}

func (s *VersionService) runPublishTx(ctx context.Context, in publishTxInput) error {
	if err := s.versions.Publish(ctx, in.tenantID, in.versionID, in.recordVersion, in.versionNumber, in.compiledJSON, in.artifactHash, in.moduleRefs); err != nil {
		return err
	}
	if err := s.assignees.DeleteByVersion(ctx, in.tenantID, in.versionID); err != nil {
		return err
	}
	if len(in.assignees) > 0 {
		if err := s.assignees.BulkInsert(ctx, in.tenantID, in.versionID, in.assignees); err != nil {
			return err
		}
	}
	return s.workflows.UpdateActiveVersion(ctx, in.tenantID, in.workflowID, &in.versionID)
}

const maxEligibilityConcurrency = 8

type eligibilityCheck struct {
	assigneeID string
	userID     uuid.UUID
	deptID     string
	role       string
}

func distinctEligibilityChecks(plan *dsl.CompiledPlan) ([]eligibilityCheck, error) {
	var checks []eligibilityCheck
	seen := map[eligibilityCheck]bool{}
	for _, dept := range plan.Departments {
		for _, stage := range dept.Stages {
			for _, assigneeID := range stage.DefaultAssignees {
				userID, err := uuid.Parse(assigneeID)
				if err != nil || userID.Version() != 7 {
					return nil, fmt.Errorf("invalid assignee UUID %s: %w", assigneeID, domain.ErrAssigneeIneligible)
				}
				check := eligibilityCheck{assigneeID: assigneeID, userID: userID, deptID: dept.IAMDepartmentID, role: stage.Role}
				if !seen[check] {
					seen[check] = true
					checks = append(checks, check)
				}
			}
		}
	}
	return checks, nil
}

func (s *VersionService) checkAssigneeEligibility(
	ctx context.Context,
	tenantID uuid.UUID,
	plan *dsl.CompiledPlan,
) error {
	checks, err := distinctEligibilityChecks(plan)
	if err != nil {
		return err
	}
	results := make([]error, len(checks))
	slots := make(chan struct{}, maxEligibilityConcurrency)
	var wg sync.WaitGroup
	for i, check := range checks {
		slots <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			results[i] = s.checkEligibility(ctx, tenantID, check)
		}()
	}
	wg.Wait()
	for _, err := range results {
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *VersionService) checkEligibility(ctx context.Context, tenantID uuid.UUID, check eligibilityCheck) error {
	eligible, err := s.membership.CheckEligibility(ctx, tenantID, check.userID, check.deptID, check.role)
	if err != nil {
		return fmt.Errorf("check eligibility: %w", err)
	}
	if !eligible {
		return fmt.Errorf("user %s dept %s role %s: %w", check.assigneeID, check.deptID, check.role, domain.ErrAssigneeIneligible)
	}
	return nil
}

func extractAssignees(
	tenantID, versionID uuid.UUID,
	plan *dsl.CompiledPlan,
) []*domain.NodeAssignee {
	var assignees []*domain.NodeAssignee
	for _, dept := range plan.Departments {
		for _, stage := range dept.Stages {
			nodeKey := dsl.NodeKey(dept.ID, &stage)
			deptUUID, err := uuid.Parse(dept.IAMDepartmentID)
			if err != nil {
				continue
			}
			for _, assigneeID := range stage.DefaultAssignees {
				userID, err := uuid.Parse(assigneeID)
				if err != nil {
					continue
				}
				assigneeRowID, err := uuid.NewV7()
				if err != nil {
					continue
				}
				assignees = append(assignees, &domain.NodeAssignee{
					ID:                assigneeRowID,
					TenantID:          tenantID,
					WorkflowVersionID: versionID,
					NodeKey:           nodeKey,
					UserID:            userID,
					DepartmentID:      deptUUID,
					Role:              stage.Role,
				})
			}
		}
	}
	return assignees
}

func expandedMainPlan(collab *dsl.CompiledCollaboration) (*dsl.CompiledPlan, error) {
	expanded, err := dsl.ExpandCalls(collab, domain.MaxPlanStages)
	if err != nil {
		return nil, fmt.Errorf("expand calls: %w", err)
	}
	for _, p := range expanded.Plans {
		if p != nil && p.Name == expanded.MainPlan {
			return p, nil
		}
	}
	return nil, fmt.Errorf("main plan %q not among the compiled plans", collab.MainPlan)
}

func storedMainPlan(compiledPlanJSON string) (*dsl.CompiledPlan, bool) {
	var collab dsl.CompiledCollaboration
	if err := json.Unmarshal([]byte(compiledPlanJSON), &collab); err != nil {
		return nil, false
	}
	p, err := expandedMainPlan(&collab)
	return p, err == nil
}

func marshalPlan(collab *dsl.CompiledCollaboration) (string, error) {
	b, err := json.Marshal(collab)
	if err != nil {
		return "", fmt.Errorf("marshal plan: %w", err)
	}
	return string(b), nil
}
