package handler_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListEndpoints_RefuseBadPaging(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	paths := []string{
		"/api/v1/workflows",
		"/api/v1/workflows/" + testWFID.String() + "/versions",
	}
	for _, path := range paths {
		for _, tc := range []struct{ query, param string }{
			{"?page=abc", "page"},
			{"?page=0", "page"},
			{"?page=-3", "page"},
			{"?page=100001", "page"},
			{"?limit=0", "limit"},
			{"?limit=500", "limit"},
			{"?limit=ten", "limit"},
		} {
			t.Run(path+tc.query, func(t *testing.T) {
				w := do(newRouter(h), req(http.MethodGet, path+tc.query, nil))

				require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
				var resp struct {
					Code          string `json:"code"`
					InvalidParams []struct {
						Name string `json:"name"`
					} `json:"invalid_params"`
				}
				require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
				assert.Equal(t, "INVALID_INPUT", resp.Code)
				require.Len(t, resp.InvalidParams, 1)
				assert.Equal(t, tc.param, resp.InvalidParams[0].Name)
			})
		}
	}
}

func TestListEndpoints_ReportEveryBadParameter(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows?page=x&limit=y", nil))
	require.Equal(t, http.StatusBadRequest, w.Code)
	var resp struct {
		InvalidParams []struct{ Name string } `json:"invalid_params"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.InvalidParams, 2)
}

func TestListEndpoints_AcceptTheBoundaries(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	for _, query := range []string{"", "?page=1&limit=1", "?page=100000&limit=100"} {
		w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows"+query, nil))
		assert.Equal(t, http.StatusOK, w.Code, query)
	}
}

func TestGetWorkflow_RefusesABadVersionsLimit(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	for _, query := range []string{"?versions_limit=-1", "?versions_limit=99999", "?versions_limit=abc", "?versions_limit=0"} {
		w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+query, nil))
		assert.Equal(t, http.StatusBadRequest, w.Code, query)
	}
}
