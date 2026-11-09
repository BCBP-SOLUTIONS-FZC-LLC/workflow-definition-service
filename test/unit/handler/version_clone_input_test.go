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
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func cloneVersionPath() string {
	return "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/clone"
}

func rawCloneReq(body string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, cloneVersionPath(), strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", testTenantID.String())
	r.Header.Set("x-user-id", testUserID.String())
	r.Header.Set("x-tenant-roles", "tenant_admin")
	return r
}

type cloneCall struct {
	calls int
	got   service.CloneReq
}

func (c *cloneCall) fake() *fakeVersionSvc {
	return &fakeVersionSvc{
		clone: func(_ context.Context, _, _, _, _ uuid.UUID, _ string, r service.CloneReq) (*domain.Workflow, *domain.WorkflowVersion, error) {
			c.calls++
			c.got = r
			return newWorkflow(), newDraftVersion(), nil
		},
	}
}

type invalidParamBody struct {
	Name   string
	Reason string
}

func invalidParamsOf(t *testing.T, prob map[string]any) []invalidParamBody {
	t.Helper()
	raw, _ := prob["invalid_params"].([]any)
	var out []invalidParamBody
	for _, item := range raw {
		m, ok := item.(map[string]any)
		require.True(t, ok)
		name, _ := m["name"].(string)
		reason, _ := m["reason"].(string)
		out = append(out, invalidParamBody{Name: name, Reason: reason})
	}
	return out
}

func TestCloneVersion_OverLongFields_Returns422(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		want []invalidParamBody
	}{
		{
			name: "new_key of 256 characters",
			body: map[string]any{"new_key": strings.Repeat("k", 256), "new_name": "n"},
			want: []invalidParamBody{{"new_key", "must not exceed 255 characters"}},
		},
		{
			name: "new_name of 256 characters",
			body: map[string]any{"new_key": "k", "new_name": strings.Repeat("n", 256)},
			want: []invalidParamBody{{"new_name", "must not exceed 255 characters"}},
		},
		{
			name: "new_description of 2001 characters",
			body: map[string]any{"new_key": "k", "new_name": "n", "new_description": strings.Repeat("d", 2001)},
			want: []invalidParamBody{{"new_description", "must not exceed 2000 characters"}},
		},
		{
			name: "256 multi-byte characters are counted as characters",
			body: map[string]any{"new_key": strings.Repeat("é", 256), "new_name": "n"},
			want: []invalidParamBody{{"new_key", "must not exceed 255 characters"}},
		},
		{
			name: "every offending field is listed in one response",
			body: map[string]any{
				"new_key":         strings.Repeat("k", 256),
				"new_name":        strings.Repeat("n", 256),
				"new_description": strings.Repeat("d", 2001),
			},
			want: []invalidParamBody{
				{"new_key", "must not exceed 255 characters"},
				{"new_name", "must not exceed 255 characters"},
				{"new_description", "must not exceed 2000 characters"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := &cloneCall{}
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, calls.fake(), &fakeValidationSvc{})

			w := do(newRouter(h), adminReq(http.MethodPost, cloneVersionPath(), tc.body))

			require.Equal(t, http.StatusUnprocessableEntity, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "INVALID_INPUT", prob["code"])
			assert.Equal(t, tc.want, invalidParamsOf(t, prob))
			assert.Zero(t, calls.calls, "an over-long request must not reach the service")
		})
	}
}

func TestCloneVersion_FieldsAtTheLimit_Returns201(t *testing.T) {
	calls := &cloneCall{}
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, calls.fake(), &fakeValidationSvc{})
	body := map[string]any{
		"new_key":         strings.Repeat("k", 255),
		"new_name":        strings.Repeat("é", 255),
		"new_description": strings.Repeat("d", 2000),
	}

	w := do(newRouter(h), adminReq(http.MethodPost, cloneVersionPath(), body))

	require.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, 1, calls.calls)
	assert.Equal(t, service.CloneReq{
		NewKey:         strings.Repeat("k", 255),
		NewName:        strings.Repeat("é", 255),
		NewDescription: strings.Repeat("d", 2000),
	}, calls.got)
}

func TestCloneVersion_MalformedBody_Returns400(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantDetail string
		wantParams []invalidParamBody
	}{
		{name: "invalid JSON", body: `{"new_key": `, wantDetail: "request body is not valid JSON"},
		{name: "empty body", body: ``, wantDetail: "request body is not valid JSON"},
		{
			name:       "trailing data after the JSON value",
			body:       `{"new_key":"k","new_name":"n"} {"new_key":"x"}`,
			wantDetail: "request body is not valid JSON",
		},
		{
			name:       "new_key as a number",
			body:       `{"new_key":42,"new_name":"n"}`,
			wantDetail: "request body has a field of the wrong type",
			wantParams: []invalidParamBody{{"new_key", "has the wrong type"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := &cloneCall{}
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, calls.fake(), &fakeValidationSvc{})

			w := do(newRouter(h), rawCloneReq(tc.body))

			require.Equal(t, http.StatusBadRequest, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, tc.wantDetail, prob["detail"])
			assert.Equal(t, tc.wantParams, invalidParamsOf(t, prob))
			assert.Zero(t, calls.calls, "a malformed request must not reach the service")
		})
	}
}
