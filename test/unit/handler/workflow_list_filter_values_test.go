package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type listFilterAnswer struct {
	Code          string `json:"code"`
	Detail        string `json:"detail"`
	InvalidParams []struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
	} `json:"invalid_params"`
}

func TestListWorkflows_AFilterValueOutsideTheAcceptedSet_IsRefusedNamingTheParameter(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		query string
		want  []string
	}{
		{"is_valid yes", "?is_valid=yes", []string{"is_valid"}},
		{"is_valid upper case", "?is_valid=TRUE", []string{"is_valid"}},
		{"is_valid capitalised", "?is_valid=False", []string{"is_valid"}},
		{"is_valid one", "?is_valid=1", []string{"is_valid"}},
		{"is_valid with a space", "?is_valid=%20true", []string{"is_valid"}},
		{"has_draft typo", "?has_draft=ture", []string{"has_draft"}},
		{"has_draft no", "?has_draft=no", []string{"has_draft"}},
		{"status upper case", "?status=ARCHIVED", []string{"status"}},
		{"status capitalised", "?status=Active", []string{"status"}},
		{"status unknown word", "?status=deleted", []string{"status"}},
		{"status all", "?status=all", []string{"status"}},
		{"every bad filter is named", "?is_valid=x&has_draft=y&status=z", []string{"is_valid", "has_draft", "status"}},
		{"a good filter beside a bad one", "?is_valid=true&status=nope", []string{"status"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			h := newHandler(&fakeWorkflowSvc{
				list: func(context.Context, uuid.UUID, port.WorkflowFilter) ([]*domain.Workflow, int64, error) {
					called = true
					return nil, 0, nil
				},
			}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

			w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows"+tc.query, nil))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
			var resp listFilterAnswer
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, "INVALID_INPUT", resp.Code)
			assert.Equal(t, "query parameters are invalid", resp.Detail)
			var names []string
			for _, p := range resp.InvalidParams {
				names = append(names, p.Name)
				assert.NotEmpty(t, p.Reason)
			}
			assert.Equal(t, tc.want, names)
			assert.False(t, called, "a refused filter never reaches the service")
		})
	}
}

func TestListWorkflows_TheAcceptedFilterValues_AreReadExactly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		query        string
		wantIsValid  *bool
		wantHasDraft *bool
		wantArchived *bool
	}{
		{"?is_valid=true", queryPtr(true), nil, nil},
		{"?is_valid=false", queryPtr(false), nil, nil},
		{"?has_draft=true", nil, queryPtr(true), nil},
		{"?has_draft=false", nil, queryPtr(false), nil},
		{"?status=active", nil, nil, queryPtr(false)},
		{"?status=archived", nil, nil, queryPtr(true)},
		{"?is_valid=true&has_draft=false&status=archived", queryPtr(true), queryPtr(false), queryPtr(true)},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			t.Parallel()

			got := listWithCapturedFilter(t, "/api/v1/workflows"+tc.query)

			assert.Equal(t, tc.wantIsValid, got.IsValid)
			assert.Equal(t, tc.wantHasDraft, got.HasDraft)
			assert.Equal(t, tc.wantArchived, got.Archived)
		})
	}
}
