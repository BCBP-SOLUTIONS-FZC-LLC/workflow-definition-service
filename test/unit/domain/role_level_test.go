package domain_test

import (
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestValidRoleLevel(t *testing.T) {
	tests := []struct {
		role string
		want bool
	}{
		{"preparator", true},
		{"reviewer", true},
		{"approver", true},
		{"", false},
		{"preparer", false},
		{"Reviewer", false},
		{" reviewer", false},
		{"engineering", false},
		{"reviewer,approver", false},
	}
	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			if got := domain.ValidRoleLevel(tt.role); got != tt.want {
				t.Errorf("ValidRoleLevel(%q) = %v, want %v", tt.role, got, tt.want)
			}
		})
	}
}
