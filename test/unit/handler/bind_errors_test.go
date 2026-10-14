package handler_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
)

func TestBindErrors_NameFieldsAndNothingElse(t *testing.T) {
	for _, tc := range []struct {
		name       string
		body       string
		wantFields []string
		secret     string
	}{
		{"missing required fields", `{}`, []string{"key", "name", "bpmn_xml"}, ""},
		{"a field of the wrong type", `{"key":"secret-value-7","name":{"a":"hunter2"},"bpmn_xml":"x"}`, []string{"name"}, "hunter2"},
		{"not json", `{"key": `, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := &fakeLogger{}
			h := handler.New(handler.Services{Workflows: &fakeWorkflowSvc{}, Log: log})
			r, _ := http.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader(tc.body))
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("x-tenant-id", testTenantID.String())
			r.Header.Set("x-user-id", testUserID.String())
			r.Header.Set("x-tenant-roles", "tenant_admin")

			w := do(newRouter(h), r)

			require.Equal(t, http.StatusBadRequest, w.Code)
			body := w.Body.String()
			for _, leak := range []string{"createWorkflowReq", "validation for", "Go struct", "cannot unmarshal", "hunter2", "secret-value-7"} {
				assert.NotContains(t, body, leak)
			}
			var resp struct {
				Code          string `json:"code"`
				InvalidParams []struct {
					Name string `json:"name"`
				} `json:"invalid_params"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
			assert.Equal(t, "BAD_REQUEST", resp.Code)
			var got []string
			for _, p := range resp.InvalidParams {
				got = append(got, p.Name)
			}
			assert.ElementsMatch(t, tc.wantFields, got)
			assert.Len(t, log.warnCalls, 1, "the raw bind error is logged, not returned")
		})
	}
}
