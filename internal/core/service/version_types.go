package service

import (
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type DiffResult struct {
	WorkflowID      uuid.UUID
	BaseVersionID   uuid.UUID
	TargetVersionID uuid.UUID
	ChangeType      string // "STRUCTURAL" | "METADATA_ONLY"
	Changes         DiffChanges
}

type DiffChanges struct {
	AddedDepartments   []string
	RemovedDepartments []string
	StepChanges        []StepChange
	MetadataOnly       bool
}

type StepChange struct {
	Description string
	Before      *domain.ExecutionStep
	After       *domain.ExecutionStep
}

type CloneReq struct {
	NewKey         string
	NewName        string
	NewDescription string
}
