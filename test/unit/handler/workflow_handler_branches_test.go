package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCreateWorkflow_MissingCtx(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	body := map[string]any{"business_key": "x", "name": "y", "bpmn_xml": "<x/>"}
	w := do(newBareRouter(h), req(http.MethodPost, "/api/v1/workflows", body))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetWorkflow_MissingCtx(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newBareRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String(), nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestArchiveWorkflow_MissingCtx(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newBareRouter(h), req(http.MethodPost, "/api/v1/workflows/"+testWFID.String()+"/archive", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
