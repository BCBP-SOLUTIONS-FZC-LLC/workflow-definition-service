package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type planNodeDeptResp struct {
	ID              string `json:"id"`
	Label           string `json:"label"`
	IAMDepartmentID string `json:"iam_department_id,omitempty"`
}

type planNodeResp struct {
	NodeKey        string           `json:"node_key"`
	NodeID         string           `json:"node_id"`
	Name           string           `json:"name,omitempty"`
	StageType      string           `json:"stage_type"`
	RoleLevel      string           `json:"role_level"`
	Department     planNodeDeptResp `json:"department"`
	DefaultUserIDs []string         `json:"default_user_ids"`
}

type planWaitResp struct {
	Key     string `json:"key"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

type versionNodesResp struct {
	WorkflowID    uuid.UUID      `json:"workflow_id"`
	VersionID     uuid.UUID      `json:"version_id"`
	VersionNumber *int32         `json:"version_number"`
	Nodes         []planNodeResp `json:"nodes"`
	Waits         []planWaitResp `json:"waits"`
}

func toVersionNodesResp(n *service.VersionNodes) versionNodesResp {
	nodes := make([]planNodeResp, 0, len(n.Nodes))
	for _, p := range n.Nodes {
		nodes = append(nodes, planNodeResp{
			NodeKey: p.Key, NodeID: p.NodeID, Name: p.Name, StageType: p.StageType, RoleLevel: p.Role,
			Department:     planNodeDeptResp{ID: p.DepartmentID, Label: p.DepartmentLabel, IAMDepartmentID: p.IAMDepartmentID},
			DefaultUserIDs: nonNil(p.DefaultUserIDs),
		})
	}
	waits := make([]planWaitResp, 0, len(n.Waits))
	for _, w := range n.Waits {
		waits = append(waits, planWaitResp{Key: w.Key, Kind: string(w.Kind), Message: w.Message})
	}
	return versionNodesResp{WorkflowID: n.Version.WorkflowID, VersionID: n.Version.ID, VersionNumber: n.Version.VersionNumber, Nodes: nodes, Waits: waits}
}

func (h *Handler) GetVersionNodes(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}
	workflowID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	versionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}
	n, err := h.versions.Nodes(c.Request.Context(), tenantID, workflowID, versionID)
	if err != nil {
		errResponse(c, h.log, err)
		return
	}
	c.JSON(http.StatusOK, toVersionNodesResp(n))
}

type moduleSlotResp struct {
	ID              string `json:"id"`
	Label           string `json:"label"`
	Open            bool   `json:"open"`
	IAMDepartmentID string `json:"iam_department_id,omitempty"`
}

type moduleTaskResp struct {
	AssigneeKey     string   `json:"assignee_key"`
	NodeID          string   `json:"node_id"`
	Name            string   `json:"name,omitempty"`
	Slot            string   `json:"slot,omitempty"`
	IAMDepartmentID string   `json:"iam_department_id,omitempty"`
	StageType       string   `json:"stage_type"`
	RoleLevel       string   `json:"role_level"`
	DefaultUserIDs  []string `json:"default_user_ids"`
}

type moduleContractResp struct {
	ModuleID      uuid.UUID        `json:"module_id"`
	VersionID     uuid.UUID        `json:"version_id"`
	ProcessID     string           `json:"process_id"`
	VersionNumber *int32           `json:"version_number,omitempty"`
	Slots         []moduleSlotResp `json:"slots"`
	Tasks         []moduleTaskResp `json:"tasks"`
	Messages      []string         `json:"messages"`
	ErrorCodes    []string         `json:"error_codes"`
	NamesUsers    bool             `json:"names_users"`
}

func toModuleContractResp(m *service.ModuleContract) moduleContractResp {
	slots := make([]moduleSlotResp, 0, len(m.Slots))
	for _, s := range m.Slots {
		slots = append(slots, moduleSlotResp{ID: s.ID, Label: s.Label, Open: s.Open, IAMDepartmentID: s.IAMDepartmentID})
	}
	tasks := make([]moduleTaskResp, 0, len(m.Tasks))
	for _, t := range m.Tasks {
		tasks = append(tasks, moduleTaskResp{
			AssigneeKey: t.AssigneeKey, NodeID: t.NodeID, Name: t.Name, Slot: t.Slot, IAMDepartmentID: t.IAMDepartmentID,
			StageType: t.StageType, RoleLevel: t.Role, DefaultUserIDs: nonNil(t.DefaultUserIDs),
		})
	}
	return moduleContractResp{
		ModuleID: m.Version.ModuleID, VersionID: m.Version.ID, ProcessID: m.Version.ProcessID, VersionNumber: m.Version.VersionNumber,
		Slots: slots, Tasks: tasks, Messages: nonNil(m.Messages), ErrorCodes: nonNil(m.ErrorCodes), NamesUsers: m.NamesUsers,
	}
}

func (h *Handler) GetModuleContract(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}
	moduleID, ok := parseUUIDParam(c, "id")
	if !ok {
		return
	}
	versionID, ok := parseUUIDParam(c, "version_id")
	if !ok {
		return
	}
	m, err := h.modules.Contract(c.Request.Context(), tenantID, moduleID, versionID)
	if err != nil {
		errResponse(c, h.log, err)
		return
	}
	c.JSON(http.StatusOK, toModuleContractResp(m))
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
