package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetDraft_MissingCtx(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newBareRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+"/draft", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestInitDraft_MissingCtx(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newBareRouter(h), req(http.MethodPost, "/api/v1/workflows/"+testWFID.String()+"/draft", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestUpdateDraft_MissingCtx(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newBareRouter(h), req(http.MethodPut, "/api/v1/workflows/"+testWFID.String()+"/draft", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestDiscardDraft_MissingCtx(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newBareRouter(h), req(http.MethodDelete, "/api/v1/workflows/"+testWFID.String()+"/draft", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
