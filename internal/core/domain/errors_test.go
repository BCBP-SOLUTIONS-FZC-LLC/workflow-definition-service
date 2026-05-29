package domain

import "testing"

func TestBPMNValidationError_Error(t *testing.T) {
	tests := []struct {
		name string
		e    BPMNValidationError
		want string
	}{
		{
			name: "with node ID",
			e:    BPMNValidationError{Code: BPMNErrCycleDetected, NodeID: "Node_1", Message: "cycle at node"},
			want: "CYCLE_DETECTED: cycle at node",
		},
		{
			name: "without node ID",
			e:    BPMNValidationError{Code: BPMNErrNoStartEvent, Message: "missing start event"},
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
		errors []BPMNValidationError
		want   string
	}{
		{
			name:   "single error",
			errors: []BPMNValidationError{{Code: BPMNErrCycleDetected, Message: "cycle"}},
			want:   "BPMN validation failed",
		},
		{
			name:   "multiple errors",
			errors: []BPMNValidationError{{Code: BPMNErrCycleDetected}, {Code: BPMNErrDanglingNode}},
			want:   "BPMN validation failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := &ValidationFailedError{Errors: tt.errors}
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
		{"ErrNotFound", ErrNotFound},
		{"ErrForbidden", ErrForbidden},
		{"ErrUnauthorized", ErrUnauthorized},
		{"ErrDraftAlreadyExists", ErrDraftAlreadyExists},
		{"ErrNoDraftExists", ErrNoDraftExists},
		{"ErrNoActiveVersion", ErrNoActiveVersion},
		{"ErrDuplicateBusinessKey", ErrDuplicateBusinessKey},
		{"ErrDraftConcurrency", ErrDraftConcurrency},
		{"ErrInvalidVersionStatus", ErrInvalidVersionStatus},
		{"ErrActiveInstancesExist", ErrActiveInstancesExist},
		{"ErrStructuralDivergence", ErrStructuralDivergence},
		{"ErrPlanQuotaExceeded", ErrPlanQuotaExceeded},
		{"ErrAssigneeIneligible", ErrAssigneeIneligible},
		{"ErrUpstreamUnavailable", ErrUpstreamUnavailable},
		{"ErrIdempotencyKeyReplay", ErrIdempotencyKeyReplay},
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
