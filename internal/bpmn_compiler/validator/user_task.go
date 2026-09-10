package validator

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const maxRoleLen = 256

// exported for white-box tests.
const MaxRoleLen = maxRoleLen

func ValidateTaskExtensions(proc *bpmncore.BPMNProcess, stageTypes map[string]bpmncore.StageTypeHandler) []domain.BPMNValidationError {
	laneRefs := bpmncore.BuildLaneRefSet(proc)
	var errs []domain.BPMNValidationError
	for _, t := range proc.UserTasks {
		errs = append(errs, ValidateTaskDef(t.ID, t.ExtensionElements, stageTypes)...)
		errs = append(errs, ValidateAssignmentDef(t.ID, t.ExtensionElements)...)
		errs = append(errs, ValidateLaneMembership(t.ID, laneRefs)...)
	}
	return errs
}

func ValidateTaskDef(taskID string, ext bpmncore.BPMNExtensionElements, stageTypes map[string]bpmncore.StageTypeHandler) []domain.BPMNValidationError {
	if ext.TaskDefinition == nil {
		return AppendErr(nil, domain.BPMNErrMissingTaskDefinition, taskID,
			"task is missing a <zeebe:taskDefinition> extension element")
	}
	if _, ok := stageTypes[ext.TaskDefinition.Type]; !ok {
		// Unknown stage types are not rejected: IAM owns role/department definitions
		// and may introduce types unknown to this service. Emit a warning and continue.
		return AppendWarn(nil, domain.BPMNWarnUnknownStageType, taskID,
			fmt.Sprintf("taskDefinition type %q is not a defined stage class in the workflow engine", ext.TaskDefinition.Type))
	}
	return nil
}

// ValidateOptionalAssignment validates candidateGroups/candidateUsers only when
// zeebe:assignmentDefinition is present. For sendTask/receiveTask assignment is optional.
func ValidateOptionalAssignment(taskID string, ext bpmncore.BPMNExtensionElements) []domain.BPMNValidationError {
	if ext.AssignmentDefinition == nil {
		return nil
	}
	return ValidateAssignmentDef(taskID, ext)
}

func ValidateAssignmentDef(taskID string, ext bpmncore.BPMNExtensionElements) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	if ext.AssignmentDefinition == nil {
		return AppendErr(errs, domain.BPMNErrMissingAssignmentDefinition, taskID,
			"userTask is missing a <zeebe:assignmentDefinition> extension element")
	}
	ad := ext.AssignmentDefinition
	if ad.CandidateGroups == "" || len(ad.CandidateGroups) > maxRoleLen {
		errs = AppendErr(errs, domain.BPMNErrCandidateGroupsEmpty, taskID,
			fmt.Sprintf("candidateGroups must be non-empty and at most %d characters", maxRoleLen))
	}
	errs = append(errs, validateCandidateUser(taskID, ad.CandidateUsers)...)
	return errs
}

func validateCandidateUser(taskID, candidateUsers string) []domain.BPMNValidationError {
	if candidateUsers == "" {
		return nil
	}
	if _, err := uuid.Parse(candidateUsers); err != nil {
		return AppendErr(nil, domain.BPMNErrInvalidCandidateUser, taskID,
			fmt.Sprintf("candidateUsers %q is not a valid UUID v7", candidateUsers))
	}
	return nil
}

// ValidateDeptID rejects a lane with no valid dept_id zeebe property — the
// real IAM department UUID eligibility checks and workflow_node_assignee
// need, distinct from the lane's name (used elsewhere as the visual/grouping id).
func ValidateDeptID(taskID string, proc *bpmncore.BPMNProcess) []domain.BPMNValidationError {
	lane := bpmncore.LaneFor(taskID, proc)
	if lane == nil {
		return nil // no lane at all is ValidateLaneMembership's TASK_NOT_IN_LANE, not this one's
	}
	deptID := bpmncore.DeptIDFor(taskID, proc)
	if deptID == "" {
		return AppendErr(nil, domain.BPMNErrMissingDeptID, taskID,
			fmt.Sprintf("lane %q has no dept_id zeebe property; add <zeebe:properties><zeebe:property name=\"dept_id\" value=\"<IAM department UUID>\"/></zeebe:properties>", lane.ID))
	}
	if _, err := uuid.Parse(deptID); err != nil {
		return AppendErr(nil, domain.BPMNErrInvalidDeptID, taskID,
			fmt.Sprintf("lane %q dept_id %q is not a valid UUID", lane.ID, deptID))
	}
	return nil
}

func ValidateLaneMembership(taskID string, laneRefs map[string]struct{}) []domain.BPMNValidationError {
	if len(laneRefs) == 0 {
		return nil
	}
	if _, ok := laneRefs[taskID]; !ok {
		return AppendErr(nil, domain.BPMNErrTaskNotInLane, taskID,
			"task is not listed in any lane's flowNodeRef list")
	}
	return nil
}
