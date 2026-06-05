package bpmn_compiler

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const (
	maxUserTasks        = 64
	maxLanes            = 16
	maxCondExpr         = 255
	maxRoleLen          = 64
	maxDefaultAssignees = 10
)

func validate(proc *bpmnProcess, g *graph) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	errs = append(errs, validateCounts(proc)...)
	errs = append(errs, validateSeqFlowRefs(proc, g)...)
	errs = append(errs, validateDanglingNodes(proc, g)...)
	errs = append(errs, validateReachability(proc, g)...)
	errs = append(errs, validateCycles(g)...)
	errs = append(errs, validateZeebeProps(proc)...)
	errs = append(errs, validateGatewayMatching(proc, g)...)
	errs = append(errs, validateConditionExprs(proc, g)...)
	return errs
}

func validateCounts(proc *bpmnProcess) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError

	switch n := len(proc.StartEvents); {
	case n == 0:
		errs = appendErr(errs, domain.BPMNErrNoStartEvent, "", "process has no start event")
	case n > 1:
		for _, e := range proc.StartEvents[1:] {
			errs = appendErr(errs, domain.BPMNErrMultipleStartEvents, e.ID,
				"only one start event is allowed")
		}
	}

	switch n := len(proc.EndEvents); {
	case n == 0:
		errs = appendErr(errs, domain.BPMNErrNoEndEvent, "", "process has no end event")
	case n > 1:
		for _, e := range proc.EndEvents[1:] {
			errs = appendErr(errs, domain.BPMNErrMultipleEndEvents, e.ID,
				"only one end event is allowed")
		}
	}

	if len(proc.UserTasks) > maxUserTasks {
		errs = appendErr(errs, domain.BPMNErrTaskLimitExceeded, "",
			fmt.Sprintf("process exceeds %d user task limit (%d found)",
				maxUserTasks, len(proc.UserTasks)))
	}

	if len(proc.LaneSet.Lanes) > maxLanes {
		errs = appendErr(errs, domain.BPMNErrLaneLimitExceeded, "",
			fmt.Sprintf("laneSet exceeds %d lane limit (%d found)",
				maxLanes, len(proc.LaneSet.Lanes)))
	}

	return errs
}

func validateSeqFlowRefs(proc *bpmnProcess, g *graph) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	for _, sf := range proc.SequenceFlows {
		if _, ok := g.nodeIDs[sf.SourceRef]; !ok {
			errs = appendErr(errs, domain.BPMNErrInvalidSequenceFlowRef, sf.ID,
				fmt.Sprintf("sourceRef %q does not reference a known node", sf.SourceRef))
		}
		if _, ok := g.nodeIDs[sf.TargetRef]; !ok {
			errs = appendErr(errs, domain.BPMNErrInvalidSequenceFlowRef, sf.ID,
				fmt.Sprintf("targetRef %q does not reference a known node", sf.TargetRef))
		}
	}
	return errs
}

func validateDanglingNodes(proc *bpmnProcess, g *graph) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError

	for _, t := range proc.UserTasks {
		errs = append(errs, checkDangling(t.ID, g)...)
	}
	for _, gw := range proc.ParallelGateways {
		errs = append(errs, checkDangling(gw.ID, g)...)
	}
	for _, gw := range proc.ExclusiveGateways {
		errs = append(errs, checkDangling(gw.ID, g)...)
	}
	for _, e := range proc.StartEvents {
		if len(g.outgoing[e.ID]) == 0 {
			errs = appendErr(errs, domain.BPMNErrDanglingNode, e.ID,
				"start event has no outgoing sequence flow")
		}
	}
	for _, e := range proc.EndEvents {
		if len(g.incoming[e.ID]) == 0 {
			errs = appendErr(errs, domain.BPMNErrDanglingNode, e.ID,
				"end event has no incoming sequence flow")
		}
	}
	return errs
}

func checkDangling(id string, g *graph) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	if len(g.incoming[id]) == 0 {
		errs = appendErr(errs, domain.BPMNErrDanglingNode, id, "node has no incoming sequence flow")
	}
	if len(g.outgoing[id]) == 0 {
		errs = appendErr(errs, domain.BPMNErrDanglingNode, id, "node has no outgoing sequence flow")
	}
	return errs
}

func validateReachability(proc *bpmnProcess, g *graph) []domain.BPMNValidationError {
	if len(proc.StartEvents) == 0 {
		return nil
	}
	visited := make(map[string]bool)
	dfs(proc.StartEvents[0].ID, g, visited)

	var errs []domain.BPMNValidationError
	for id := range g.nodeIDs {
		if !visited[id] {
			errs = appendErr(errs, domain.BPMNErrUnreachableNode, id,
				"node is not reachable from the start event")
		}
	}
	return errs
}

func validateCycles(g *graph) []domain.BPMNValidationError {
	sccs := tarjanSCC(g)
	var errs []domain.BPMNValidationError
	for _, scc := range sccs {
		if len(scc) > 1 {
			errs = appendErr(errs, domain.BPMNErrCycleDetected, strings.Join(scc, ","),
				fmt.Sprintf("cycle detected among nodes: %s", strings.Join(scc, ", ")))
		}
	}
	return errs
}

var validStageTypes = map[string]struct{}{
	"prep": {}, "review": {}, "approve": {},
}

var validAssigneeModes = map[string]struct{}{
	"any": {}, "all": {},
}

func validateZeebeProps(proc *bpmnProcess) []domain.BPMNValidationError {
	laneNames := buildLaneNameSet(proc)
	var errs []domain.BPMNValidationError
	for _, t := range proc.UserTasks {
		errs = append(errs, validateTaskProps(t, laneNames)...)
	}
	return errs
}

func buildLaneNameSet(proc *bpmnProcess) map[string]struct{} {
	m := make(map[string]struct{}, len(proc.LaneSet.Lanes))
	for _, lane := range proc.LaneSet.Lanes {
		m[normalizeLaneName(lane.Name)] = struct{}{}
	}
	return m
}

func validateTaskProps(t bpmnUserTask, laneNames map[string]struct{}) []domain.BPMNValidationError {
	props := propsMap(t.ExtensionElements)
	var errs []domain.BPMNValidationError
	errs = append(errs, validateTaskDeptID(t, props, laneNames)...)
	errs = append(errs, validateTaskStageType(t, props)...)
	errs = append(errs, validateTaskRole(t, props)...)
	errs = append(errs, validateTaskDefaultUserIDs(t, props)...)
	errs = append(errs, validateTaskOptionalProps(t, props)...)
	return errs
}

func validateTaskDeptID(t bpmnUserTask, props map[string]string, laneNames map[string]struct{}) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	deptID, ok := props["dept_id"]
	if !ok || deptID == "" {
		return appendErr(errs, domain.BPMNErrMissingZeebeProperty, t.ID,
			"missing required zeebe property: dept_id")
	}
	if _, valid := laneNames[deptID]; !valid {
		errs = appendErr(errs, domain.BPMNErrInvalidDeptID, t.ID,
			fmt.Sprintf("dept_id %q does not match any lane name (normalized)", deptID))
	}
	return errs
}

func validateTaskStageType(t bpmnUserTask, props map[string]string) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	stageType, ok := props["stage_type"]
	if !ok || stageType == "" {
		return appendErr(errs, domain.BPMNErrMissingZeebeProperty, t.ID,
			"missing required zeebe property: stage_type")
	}
	if _, valid := validStageTypes[stageType]; !valid {
		errs = appendErr(errs, domain.BPMNErrInvalidStageType, t.ID,
			fmt.Sprintf("invalid stage_type %q: must be prep, review, or approve", stageType))
	}
	return errs
}

func validateTaskRole(t bpmnUserTask, props map[string]string) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	role := props["role"]
	if role == "" {
		return appendErr(errs, domain.BPMNErrMissingZeebeProperty, t.ID,
			"missing required zeebe property: role")
	}
	if len(role) > maxRoleLen {
		errs = appendErr(errs, domain.BPMNErrRoleEmpty, t.ID,
			fmt.Sprintf("role exceeds %d character limit", maxRoleLen))
	}
	return errs
}

func validateTaskDefaultUserIDs(t bpmnUserTask, props map[string]string) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	rawIDs, ok := props["default_user_ids"]
	if !ok || rawIDs == "" {
		return appendErr(errs, domain.BPMNErrMissingZeebeProperty, t.ID,
			"missing required zeebe property: default_user_ids")
	}
	parts := strings.Split(rawIDs, ",")
	for _, rawID := range parts {
		rawID = strings.TrimSpace(rawID)
		if _, err := uuid.Parse(rawID); err != nil {
			errs = appendErr(errs, domain.BPMNErrInvalidUUID, t.ID,
				fmt.Sprintf("default_user_ids contains invalid UUID: %q", rawID))
		}
	}
	if len(parts) > maxDefaultAssignees {
		errs = appendErr(errs, domain.BPMNErrTaskAssigneeLimitExceeded, t.ID,
			fmt.Sprintf("default_user_ids contains %d entries; maximum is %d",
				len(parts), maxDefaultAssignees))
	}
	return errs
}

func validateTaskOptionalProps(t bpmnUserTask, props map[string]string) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	if mode, ok := props["assignee_mode"]; ok {
		if _, valid := validAssigneeModes[mode]; !valid {
			errs = appendErr(errs, domain.BPMNErrInvalidZeebeProperty, t.ID,
				fmt.Sprintf("invalid assignee_mode %q: must be any or all", mode))
		}
	}
	if rc, ok := props["requires_comment"]; ok {
		if _, err := strconv.ParseBool(rc); err != nil {
			errs = appendErr(errs, domain.BPMNErrInvalidZeebeProperty, t.ID,
				fmt.Sprintf("invalid requires_comment %q: must be true or false", rc))
		}
	}
	if sla, ok := props["sla_duration"]; ok {
		if !isValidSLADuration(sla) {
			errs = appendErr(errs, domain.BPMNErrInvalidSLADuration, t.ID,
				fmt.Sprintf("invalid sla_duration %q: use Go duration (24h, 90m) or day suffix (2d)", sla))
		}
	}
	return errs
}

func isValidSLADuration(s string) bool {
	if strings.HasSuffix(s, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(s, "d"))
		return err == nil && n > 0
	}
	d, err := time.ParseDuration(s)
	return err == nil && d > 0
}

func validateGatewayMatching(proc *bpmnProcess, g *graph) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	for _, gw := range proc.ParallelGateways {
		if len(g.outgoing[gw.ID]) > 1 {
			if _, ok := findJoin(gw.ID, g); !ok {
				errs = appendErr(errs, domain.BPMNErrUnmatchedGateway, gw.ID,
					"parallel split gateway has no matching join gateway")
			}
		}
	}
	for _, gw := range proc.ExclusiveGateways {
		if len(g.outgoing[gw.ID]) > 1 {
			if _, ok := findJoin(gw.ID, g); !ok {
				errs = appendErr(errs, domain.BPMNErrUnmatchedGateway, gw.ID,
					"exclusive split gateway has no matching join gateway")
			}
		}
	}
	return errs
}

func validateConditionExprs(proc *bpmnProcess, g *graph) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	for _, sf := range proc.SequenceFlows {
		if len(g.outgoing[sf.SourceRef]) < 2 {
			continue
		}
		props := propsMap(sf.ExtensionElements)
		cond, ok := props["condition_expression"]
		if !ok {
			continue
		}
		if len(cond) > maxCondExpr {
			errs = appendErr(errs, domain.BPMNErrInvalidZeebeProperty, sf.ID,
				fmt.Sprintf("condition_expression exceeds %d character limit", maxCondExpr))
		}
	}
	return errs
}

func appendErr(
	errs []domain.BPMNValidationError,
	code domain.BPMNErrorCode,
	nodeID, msg string,
) []domain.BPMNValidationError {
	return append(errs, domain.BPMNValidationError{
		Code:    code,
		NodeID:  nodeID,
		Message: msg,
	})
}
