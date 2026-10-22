package validator

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const maxRoleLen = 256

const MaxRoleLen = maxRoleLen

func ValidateTaskExtensions(proc *bpmncore.BPMNProcess, stageTypes map[string]bpmncore.StageTypeHandler) []domain.BPMNValidationError {
	laneRefs := bpmncore.BuildLaneRefSet(proc)
	var errs []domain.BPMNValidationError
	for _, t := range proc.UserTasks {
		errs = append(errs, ValidateTaskDef(t.ID, t.ExtensionElements, stageTypes)...)
		errs = append(errs, ValidateAssignmentDef(t.ID, t.ExtensionElements)...)
		errs = append(errs, ValidateRoleLevel(t.ID, t.ExtensionElements)...)
		errs = append(errs, ValidateDefaultAssignee(t.ID, t.ExtensionElements)...)
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
		return AppendWarn(nil, domain.BPMNWarnUnknownStageType, taskID,
			fmt.Sprintf("taskDefinition type %q is not a defined stage class in the workflow engine", ext.TaskDefinition.Type))
	}
	return nil
}

func ValidateOptionalAssignment(taskID string, ext bpmncore.BPMNExtensionElements) []domain.BPMNValidationError {
	if ext.AssignmentDefinition == nil {
		return nil
	}
	return ValidateAssignmentDef(taskID, ext)
}

func ValidateDefaultAssignee(taskID string, ext bpmncore.BPMNExtensionElements) []domain.BPMNValidationError {
	if ext.AssignmentDefinition == nil {
		return nil
	}
	if strings.TrimSpace(ext.AssignmentDefinition.CandidateUsers) != "" {
		return nil
	}
	return AppendWarn(nil, domain.BPMNErrCandidateUsersEmpty, taskID,
		"userTask has no candidateUsers; a workflow that runs it must name its user, here or in a calling callActivity's Assignees, before it publishes")
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

func ValidateRoleLevel(taskID string, ext bpmncore.BPMNExtensionElements) []domain.BPMNValidationError {
	if ext.AssignmentDefinition == nil {
		return nil
	}
	role := ext.AssignmentDefinition.CandidateGroups
	if role == "" || len(role) > maxRoleLen || domain.ValidRoleLevel(role) {
		return nil
	}
	return AppendErr(nil, domain.BPMNErrCandidateGroupsInvalid, taskID,
		fmt.Sprintf("candidateGroups %q is not a department role level: preparator, reviewer or approver", role))
}

func validateCandidateUser(taskID, candidateUsers string) []domain.BPMNValidationError {
	if candidateUsers == "" {
		return nil
	}
	if !IsUserID(candidateUsers) {
		return AppendErr(nil, domain.BPMNErrInvalidCandidateUser, taskID,
			fmt.Sprintf("candidateUsers %q is not a valid UUID v7", candidateUsers))
	}
	return nil
}

func IsUserID(s string) bool {
	id, err := uuid.Parse(s)
	return err == nil && id.Version() == 7
}

func ValidateDeptID(taskID string, proc *bpmncore.BPMNProcess, defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	lane := bpmncore.LaneFor(taskID, proc)
	if lane == nil {
		return nil
	}
	deptID := bpmncore.DeptIDFor(taskID, proc)
	if deptID == "" && defs != nil && defs.ModuleProcesses[proc.ID] {
		return nil
	}
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
