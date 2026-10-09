package bpmn_compiler

import (
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type StageTypeHandler = bpmncore.StageTypeHandler

type prepStage struct{}

func (prepStage) ID() string           { return "prep" }
func (prepStage) ActivityName() string { return "PrepActivity" }
func (prepStage) ValidateProps(_ string, _ map[string]string) []domain.BPMNValidationError {
	return nil
}

type reviewStage struct{}

func (reviewStage) ID() string           { return "review" }
func (reviewStage) ActivityName() string { return "ReviewActivity" }
func (reviewStage) ValidateProps(_ string, _ map[string]string) []domain.BPMNValidationError {
	return nil
}

type approveStage struct{}

func (approveStage) ID() string           { return "approve" }
func (approveStage) ActivityName() string { return "ApproveActivity" }
func (approveStage) ValidateProps(_ string, _ map[string]string) []domain.BPMNValidationError {
	return nil
}

func defaultStageTypes() map[string]bpmncore.StageTypeHandler {
	return map[string]bpmncore.StageTypeHandler{
		"prep":    prepStage{},
		"review":  reviewStage{},
		"approve": approveStage{},
	}
}
