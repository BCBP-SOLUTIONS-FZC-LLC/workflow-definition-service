package handler_test

import (
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
)

func TestExportedErrorCodes(t *testing.T) {
	tests := []struct {
		name string
		code handler.ErrCode
		want string
	}{
		{"CodeNotFound", handler.CodeNotFound, "NOT_FOUND"},
		{"CodeUnauthorized", handler.CodeUnauthorized, "UNAUTHORIZED"},
		{"CodeForbidden", handler.CodeForbidden, "FORBIDDEN"},
		{"CodeDraftAlreadyExists", handler.CodeDraftAlreadyExists, "DRAFT_ALREADY_EXISTS"},
		{"CodeDraftNotFound", handler.CodeDraftNotFound, "DRAFT_NOT_FOUND"},
		{"CodeNoActiveVersion", handler.CodeNoActiveVersion, "NO_ACTIVE_VERSION"},
		{"CodeDuplicateKey", handler.CodeDuplicateKey, "DUPLICATE_BUSINESS_KEY"},
		{"CodeDraftConcurrency", handler.CodeDraftConcurrency, "DRAFT_CONCURRENCY"},
		{"CodeInvalidStatus", handler.CodeInvalidStatus, "INVALID_VERSION_STATUS"},
		{"CodeActiveInstances", handler.CodeActiveInstances, "ACTIVE_INSTANCES_EXIST"},
		{"CodeStructuralDiv", handler.CodeStructuralDiv, "STRUCTURAL_DIVERGENCE"},
		{"CodePlanQuota", handler.CodePlanQuota, "PLAN_QUOTA_EXCEEDED"},
		{"CodeAssigneeIneligible", handler.CodeAssigneeIneligible, "ASSIGNEE_INELIGIBLE"},
		{"CodeUpstream", handler.CodeUpstream, "UPSTREAM_UNAVAILABLE"},
		{"CodeIdempotencyReplay", handler.CodeIdempotencyReplay, "IDEMPOTENCY_KEY_REPLAY"},
		{"CodeBPMNValidation", handler.CodeBPMNValidation, "BPMN_VALIDATION_FAILED"},
		{"CodeInternal", handler.CodeInternal, "INTERNAL_ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.code) != tt.want {
				t.Errorf("got error code %q, want %q", tt.code, tt.want)
			}
		})
	}
}
