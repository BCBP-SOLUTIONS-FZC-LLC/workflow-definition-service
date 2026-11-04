package service

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

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

type WaitKind string

const (
	WaitReceiveTask     WaitKind = "receive_task"
	WaitMessageBoundary WaitKind = "message_boundary"
)

type PlanWait struct {
	Key     string
	Kind    WaitKind
	Message string
}

type VersionNodes struct {
	Version *domain.WorkflowVersion
	Nodes   []PlanNode
	Waits   []PlanWait
}

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
	for _, w := range dsl.ExternalWaits(&dsl.CompiledCollaboration{MainPlan: main.Name, Plans: []*dsl.CompiledPlan{main}}) {
		kind := WaitReceiveTask
		if w.Boundary {
			kind = WaitMessageBoundary
		}
		out.Waits = append(out.Waits, PlanWait{Key: w.Key, Kind: kind, Message: w.Message})
	}
	return out, nil
}
