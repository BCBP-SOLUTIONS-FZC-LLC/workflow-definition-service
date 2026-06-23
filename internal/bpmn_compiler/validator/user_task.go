package validator

import (
	"fmt"
	"strconv"

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
		errs = append(errs, ValidateRequiresComment(t.ID, t.ExtensionElements)...)
	}
	return errs
}

func ValidateTaskDef(taskID string, ext bpmncore.BPMNExtensionElements, stageTypes map[string]bpmncore.StageTypeHandler) []domain.BPMNValidationError {
	if ext.TaskDefinition == nil {
		return AppendErr(nil, domain.BPMNErrMissingTaskDefinition, taskID,
			"task is missing a <zeebe:taskDefinition> extension element")
	}
	if _, ok := stageTypes[ext.TaskDefinition.Type]; !ok {
		return AppendErr(nil, domain.BPMNErrInvalidTaskDefinitionType, taskID,
			fmt.Sprintf("taskDefinition type %q is not a registered stage type", ext.TaskDefinition.Type))
	}
	return nil
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

func ValidateLaneMembership(taskID string, laneRefs map[string]struct{}) []domain.BPMNValidationError {
	if _, ok := laneRefs[taskID]; !ok {
		return AppendErr(nil, domain.BPMNErrTaskNotInLane, taskID,
			"task is not listed in any lane's flowNodeRef list")
	}
	return nil
}

func ValidateRequiresComment(taskID string, ext bpmncore.BPMNExtensionElements) []domain.BPMNValidationError {
	for _, p := range ext.ZeebeProps.Items {
		if p.Name == "requires_comment" {
			if _, err := strconv.ParseBool(p.Value); err != nil {
				return AppendErr(nil, domain.BPMNErrMissingTaskDefinition, taskID,
					fmt.Sprintf("invalid requires_comment %q: must be true or false", p.Value))
			}
			return nil
		}
	}
	return nil
}
