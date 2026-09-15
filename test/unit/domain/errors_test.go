package domain_test

import (
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestBPMNValidationError_Error(t *testing.T) {
	tests := []struct {
		name string
		e    domain.BPMNValidationError
		want string
	}{
		{
			name: "with node ID",
			e:    domain.BPMNValidationError{Code: domain.BPMNErrCycleDetected, NodeID: "Node_1", Message: "cycle at node"},
			want: "CYCLE_DETECTED: cycle at node",
		},
		{
			name: "without node ID",
			e:    domain.BPMNValidationError{Code: domain.BPMNErrNoStartEvent, Message: "missing start event"},
			want: "NO_START_EVENT: missing start event",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.e.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidationFailedError_Error(t *testing.T) {
	tests := []struct {
		name   string
		errors []domain.BPMNValidationError
		want   string
	}{
		{
			name:   "single error",
			errors: []domain.BPMNValidationError{{Code: domain.BPMNErrCycleDetected, Message: "cycle"}},
			want:   "BPMN validation failed",
		},
		{
			name:   "multiple errors",
			errors: []domain.BPMNValidationError{{Code: domain.BPMNErrCycleDetected}, {Code: domain.BPMNErrDanglingNode}},
			want:   "BPMN validation failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &domain.ValidationFailedError{Errors: tt.errors}
			if got := e.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSentinelErrors_AreDistinct(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"ErrNotFound", domain.ErrNotFound},
		{"ErrForbidden", domain.ErrForbidden},
		{"ErrUnauthorized", domain.ErrUnauthorized},
		{"ErrDraftAlreadyExists", domain.ErrDraftAlreadyExists},
		{"ErrNoDraftExists", domain.ErrNoDraftExists},
		{"ErrNoActiveVersion", domain.ErrNoActiveVersion},
		{"ErrDuplicateBusinessKey", domain.ErrDuplicateBusinessKey},
		{"ErrDraftConcurrency", domain.ErrDraftConcurrency},
		{"ErrInvalidVersionStatus", domain.ErrInvalidVersionStatus},
		{"ErrActiveInstancesExist", domain.ErrActiveInstancesExist},
		{"ErrStructuralDivergence", domain.ErrStructuralDivergence},
		{"ErrPlanQuotaExceeded", domain.ErrPlanQuotaExceeded},
		{"ErrAssigneeIneligible", domain.ErrAssigneeIneligible},
		{"ErrUpstreamUnavailable", domain.ErrUpstreamUnavailable},
		{"ErrIdempotencyKeyReplay", domain.ErrIdempotencyKeyReplay},
	}
	seen := make(map[string]string)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := tt.err.Error()
			if prev, exists := seen[msg]; exists {
				t.Errorf("sentinel %q has same message as %q: %q", tt.name, prev, msg)
			}
			seen[msg] = tt.name
		})
	}
}
