package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func validateRawReq(rawBody string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/validate", strings.NewReader(rawBody))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", testTenantID.String())
	r.Header.Set("x-user-id", testUserID.String())
	return r
}

func TestValidateBPMN_UnreadableBody_Returns400(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		body       string
		wantDetail string
		wantParams []map[string]any
	}{
		{
			name:       "invalid JSON",
			body:       `{"bpmn_xml": `,
			wantDetail: "request body is not valid JSON",
		},
		{
			name:       "empty body",
			body:       ``,
			wantDetail: "request body is not valid JSON",
		},
		{
			name:       "whitespace-only body",
			body:       " \n\t ",
			wantDetail: "request body is not valid JSON",
		},
		{
			name:       "trailing data after the JSON value",
			body:       `{"bpmn_xml":"<definitions/>"} {"bpmn_xml":"<other/>"}`,
			wantDetail: "request body is not valid JSON",
		},
		{
			name:       "bpmn_xml of the wrong JSON type",
			body:       `{"bpmn_xml": 42}`,
			wantDetail: "request body has a field of the wrong type",
			wantParams: []map[string]any{{"name": "bpmn_xml", "reason": "has the wrong type"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{
				validate: func(context.Context, uuid.UUID, string) (bool, []domain.BPMNValidationError, error) {
					called = true
					return true, nil, nil
				},
			})

			w := do(newRouter(h), validateRawReq(tc.body))

			require.Equal(t, http.StatusBadRequest, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, tc.wantDetail, prob["detail"])
			if tc.wantParams == nil {
				assert.Empty(t, prob["invalid_params"])
			} else {
				assert.Equal(t, toAnySlice(tc.wantParams), prob["invalid_params"])
			}
			assert.False(t, called, "an unreadable body never reaches the validation service")
		})
	}
}

func toAnySlice(in []map[string]any) []any {
	out := make([]any, len(in))
	for i, m := range in {
		out[i] = m
	}
	return out
}
