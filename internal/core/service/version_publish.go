package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

type publishTxInput struct {
	tenantID      uuid.UUID
	workflowID    uuid.UUID
	versionID     uuid.UUID
	versionNumber int32
	compiledJSON  string
	artifactHash  string
	assignees     []*domain.NodeAssignee
	moduleRefs    []domain.ModuleRef
}

// publishPrep is what publish computes before its transaction.
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
	collab, err := s.compiler.CompileForPublish(ctx, bundledXML)
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
		return nil // first publish. no divergence possible
	}

	active, err := s.versions.GetByID(ctx, tenantID, *wf.ActiveVersionID)
	if err != nil {
		return fmt.Errorf("get active version for divergence check: %w", err)
	}

	if active.CompiledPlanJSON == nil || *active.CompiledPlanJSON == "" {
		return nil // no stored baseline to compare; allow publish
	}
	activePlan, ok := storedMainPlan(*active.CompiledPlanJSON)
	if !ok {
		return nil // unreadable baseline; allow publish rather than blocking
	}

	diff := computeDiff(activePlan, draftPlan)
	if !diff.MetadataOnly && !forcePublishStructural {
		return domain.ErrStructuralDivergence
	}
	return nil
}

func (s *VersionService) runPublishTx(ctx context.Context, in publishTxInput) error {
	if err := s.versions.Publish(ctx, in.tenantID, in.versionID, in.versionNumber, in.compiledJSON, in.artifactHash, in.moduleRefs); err != nil {
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

func (s *VersionService) checkAssigneeEligibility(
	ctx context.Context,
	tenantID uuid.UUID,
	plan *dsl.CompiledPlan,
) error {
	for _, dept := range plan.Departments {
		for _, stage := range dept.Stages {
			if err := s.checkStageEligibility(ctx, tenantID, dept.IAMDepartmentID, stage); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *VersionService) checkStageEligibility(
	ctx context.Context,
	tenantID uuid.UUID,
	deptID string,
	stage dsl.StageDef,
) error {
	for _, assigneeID := range stage.DefaultAssignees {
		userID, err := uuid.Parse(assigneeID)
		if err != nil || userID.Version() != 7 {
			return fmt.Errorf("invalid assignee UUID %s: %w", assigneeID, domain.ErrAssigneeIneligible)
		}
		eligible, err := s.membership.CheckEligibility(ctx, tenantID, userID, deptID, stage.Role)
		if err != nil {
			return fmt.Errorf("check eligibility: %w", err)
		}
		if !eligible {
			return fmt.Errorf("user %s dept %s role %s: %w", assigneeID, deptID, stage.Role, domain.ErrAssigneeIneligible)
		}
	}
	return nil
}

// stageNodeSuffix mirrors the Execution Service's own node-key composition, so
// one node carries one name across both services. The BPMN element id rather
// than the stage type, because a department may hold two stages of the same type.
func stageNodeSuffix(stage dsl.StageDef) string {
	if stage.NodeID != "" {
		return stage.NodeID
	}
	return stage.Type
}

func extractAssignees(
	tenantID, versionID uuid.UUID,
	plan *dsl.CompiledPlan,
) []*domain.NodeAssignee {
	var assignees []*domain.NodeAssignee
	for _, dept := range plan.Departments {
		for _, stage := range dept.Stages {
			nodeKey := dept.ID + "/" + stageNodeSuffix(stage)
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

// expandedMainPlan is the main plan with its calls expanded, as the Execution
// Service runs it: every task has its call-scoped node key and department.
func expandedMainPlan(collab *dsl.CompiledCollaboration) (*dsl.CompiledPlan, error) {
	expanded, err := dsl.ExpandCalls(collab, 0)
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

// storedMainPlan reads compiled_plan_json as publish stores it, expanded. ok
// is false when the blob does not decode or does not expand.
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
