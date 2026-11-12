package handler_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestValidateBPMN_ContextErrors_AreMappedByTheErrorMapper(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "deadline passed while parsing",
			err:        fmt.Errorf("validate bpmn: bpmn parse: %w", context.DeadlineExceeded),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
		},
		{
			name:       "client cancelled while resolving modules",
			err:        fmt.Errorf("validate bpmn: resolve modules: %w", context.Canceled),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
		},
		{
			name:       "a specific sentinel keeps its own mapping",
			err:        errors.Join(domain.ErrNotFound, context.Canceled),
			wantStatus: http.StatusNotFound,
			wantCode:   "NOT_FOUND",
		},
		{
			name:       "an unrelated error stays 500",
			err:        errors.New("boom"),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			val := &fakeValidationSvc{validate: func(context.Context, uuid.UUID, string) (bool, []domain.BPMNValidationError, error) {
				return false, nil, tc.err
			}}
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, val)

			w := do(newRouter(h), req(http.MethodPost, "/api/v1/workflows/validate", map[string]string{"bpmn_xml": "<definitions/>"}))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			assert.Equal(t, tc.wantCode, problemCode(t, w.Body.Bytes()))
		})
	}
}
