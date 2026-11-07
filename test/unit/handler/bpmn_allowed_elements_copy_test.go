package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/enums"
)

func decodeAllowedElementsBody(t *testing.T, w *httptest.ResponseRecorder) []string {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Elements []string `json:"elements"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	return resp.Elements
}

func TestAllowedBPMNElements_CallerMutatesReturnedSlice_LaterResponsesUnchanged(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(elements []string) []string
	}{
		{
			name: "overwrite every entry",
			mutate: func(elements []string) []string {
				for i := range elements {
					elements[i] = "mutated"
				}
				return elements
			},
		},
		{
			name: "reverse in place",
			mutate: func(elements []string) []string {
				for i, j := 0, len(elements)-1; i < j; i, j = i+1, j-1 {
					elements[i], elements[j] = elements[j], elements[i]
				}
				return elements
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHandler(
				&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{},
			)
			router := newRouter(h)
			const path = "/api/v1/bpmn/allowed-elements"

			before := decodeAllowedElementsBody(t, do(router, req(http.MethodGet, path, nil)))
			require.NotEmpty(t, before)
			require.Contains(t, before, "serviceTask")

			tc.mutate(enums.AllowedBPMNElements())

			after := decodeAllowedElementsBody(t, do(router, req(http.MethodGet, path, nil)))
			assert.Equal(t, before, after)
			assert.Equal(t, before, enums.AllowedBPMNElements())
		})
	}
}
