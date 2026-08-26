package handler_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/enums"
)

func TestAllowedBPMNElements_OK(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newRouter(h), req(http.MethodGet, "/api/v1/bpmn/allowed-elements", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Elements []string `json:"elements"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, enums.AllowedBPMNElements, resp.Elements)
}

func TestAllowedBPMNElements_MissingCtx(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})

	w := do(newBareRouter(h), req(http.MethodGet, "/api/v1/bpmn/allowed-elements", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
