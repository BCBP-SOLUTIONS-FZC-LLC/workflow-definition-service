package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// PlanNode is one human task of a published version's expanded main plan
// (definition LLD §3.3.12.2).
type PlanNode struct {
	Key             string
	NodeID          string
	Name            string
	StageType       string
	Role            string
	DepartmentID    string
	DepartmentLabel string
	IAMDepartmentID string
	DefaultUserIDs  []string
}

// VersionNodes is a version and its nodes, in plan order.
type VersionNodes struct {
	Version *domain.WorkflowVersion
	Nodes   []PlanNode
}

// Nodes lists the human tasks of a published or archived version's stored
// plan, its calls expanded, keyed as the Execution Service keys them.
func (s *VersionService) Nodes(ctx context.Context, tenantID, workflowID, versionID uuid.UUID) (*VersionNodes, error) {
	v, err := s.Get(ctx, tenantID, workflowID, versionID)
	if err != nil {
		return nil, err
	}
	if v.Status == domain.VersionStatusDraft || v.CompiledPlanJSON == nil {
		return nil, domain.ErrVersionNotPublished
	}
	var collab dsl.CompiledCollaboration
	if err := json.Unmarshal([]byte(*v.CompiledPlanJSON), &collab); err != nil {
		return nil, fmt.Errorf("version %s: decode stored plan: %w", v.ID, err)
	}
	main, err := expandedMainPlan(&collab)
	if err != nil {
		return nil, fmt.Errorf("version %s: %w", v.ID, err)
	}
	out := &VersionNodes{Version: v}
	for _, d := range main.Departments {
		for i := range d.Stages {
			st := &d.Stages[i]
			if !st.CreatesHumanTask() {
				continue
			}
			out.Nodes = append(out.Nodes, PlanNode{
				Key: dsl.NodeKey(d.ID, st), NodeID: st.NodeID, Name: st.Name, StageType: st.Type, Role: st.Role,
				DepartmentID: d.ID, DepartmentLabel: d.Label, IAMDepartmentID: d.IAMDepartmentID,
				DefaultUserIDs: st.DefaultAssignees,
			})
		}
	}
	return out, nil
}
