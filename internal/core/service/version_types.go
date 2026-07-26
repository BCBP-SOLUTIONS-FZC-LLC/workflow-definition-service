package service

import (
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"

	"github.com/google/uuid"
)

// NOTE: quota tiers are hardcoded until a billing service integration is available.
// When that integration lands, replace this switch with a gRPC/HTTP call to the billing service.
func workflowQuotaLimit(planTier string) int64 {
	switch strings.ToLower(planTier) {
	case "starter":
		return 5
	case "pro":
		return 50
	case "enterprise":
		return -1
	default:
		return 5
	}
}

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
	Before      *dsl.ExecutionStep
	After       *dsl.ExecutionStep
}

type CloneReq struct {
	NewKey         string
	NewName        string
	NewDescription string
}
