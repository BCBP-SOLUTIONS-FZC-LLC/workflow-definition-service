package service

import (
	"strings"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type noopLogger struct{}

func (noopLogger) Info(string, map[string]any)  { /* no-op */ }
func (noopLogger) Error(string, map[string]any) { /* no-op */ }
func (noopLogger) Fatal(string, map[string]any) { /* no-op */ }
func (noopLogger) Warn(string, map[string]any)  { /* no-op */ }
func (noopLogger) Debug(string, map[string]any) { /* no-op */ }

var _ port.Logger = noopLogger{}

func logOrNoop(l port.Logger) port.Logger {
	if l == nil {
		return noopLogger{}
	}
	return l
}

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
	Before      *domain.ExecutionStep
	After       *domain.ExecutionStep
}

type CloneReq struct {
	NewKey         string
	NewName        string
	NewDescription string
}
