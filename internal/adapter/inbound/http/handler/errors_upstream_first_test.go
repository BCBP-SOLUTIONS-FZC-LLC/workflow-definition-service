package handler

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestMapErr_AnOutageAlongsideAnyOtherCause_AlwaysAnswers503(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		other error
	}{
		{"not found", domain.ErrNotFound},
		{"no rows", pgcommon.ErrNoRows},
		{"no draft", domain.ErrNoDraftExists},
		{"no active version", domain.ErrNoActiveVersion},
		{"unauthorized", domain.ErrUnauthorized},
		{"forbidden", domain.ErrForbidden},
		{"draft already exists", domain.ErrDraftAlreadyExists},
		{"duplicate template key", domain.ErrDuplicateTemplateKey},
		{"record version required", domain.ErrRecordVersionRequired},
		{"draft concurrency", domain.ErrDraftConcurrency},
		{"invalid version status", domain.ErrInvalidVersionStatus},
		{"active instances exist", domain.ErrActiveInstancesExist},
		{"structural divergence", domain.ErrStructuralDivergence},
		{"module process id taken", domain.ErrModuleProcessIDTaken},
		{"module in use", domain.ErrModuleInUse},
		{"idempotency replay", domain.ErrIdempotencyKeyReplay},
		{"idempotency in progress", domain.ErrIdempotencyInProgress},
		{"plan quota exceeded", domain.ErrPlanQuotaExceeded},
		{"assignee ineligible", domain.ErrAssigneeIneligible},
		{"invalid connector credential input", domain.ErrInvalidConnectorCredentialInput},
		{"invalid connector alias input", domain.ErrInvalidConnectorAliasInput},
		{"BPMN validation failure", &domain.ValidationFailedError{Errors: []domain.BPMNValidationError{{Code: domain.BPMNErrNoStartEvent, NodeID: "P1"}}}},
	}
	wraps := []struct {
		name string
		join func(other error) error
	}{
		{"outage listed second", func(other error) error { return errors.Join(other, domain.ErrUpstreamUnavailable) }},
		{"outage listed first", func(other error) error { return errors.Join(domain.ErrUpstreamUnavailable, other) }},
		{"one error wrapping both", func(other error) error { return fmt.Errorf("%w: %w", other, domain.ErrUpstreamUnavailable) }},
	}
	for _, tc := range cases {
		for _, w := range wraps {
			t.Run(tc.name+"/"+w.name, func(t *testing.T) {
				t.Parallel()

				status, code, detail, params := mapErr(w.join(tc.other))

				assert.Equal(t, http.StatusServiceUnavailable, status)
				assert.Equal(t, CodeUpstream, code)
				assert.Equal(t, "an upstream dependency is temporarily unavailable", detail)
				assert.Empty(t, params)
			})
		}
	}
}
