package handler_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestListVersions_MissingCtx(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newBareRouter(h), req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+"/versions", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetVersion_MissingCtx(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String()
	w := do(newBareRouter(h), req(http.MethodGet, path, nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestPublishVersion_MissingCtx(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
	w := do(newBareRouter(h), req(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestPublishVersion_InvalidWorkflowUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/workflows/bad/versions/"+testVerID.String()+"/publish", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPublishVersion_InvalidVersionUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), adminReq(http.MethodPost, "/api/v1/workflows/"+testWFID.String()+"/versions/bad/publish", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCloneVersion_MissingCtx(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/clone"
	w := do(newBareRouter(h), req(http.MethodPost, path, map[string]any{"new_key": "x", "new_name": "y"}))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCloneVersion_InvalidWorkflowUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	path := "/api/v1/workflows/bad/versions/" + testVerID.String() + "/clone"
	w := do(newRouter(h), adminReq(http.MethodPost, path, map[string]any{"new_key": "x", "new_name": "y"}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCloneVersion_InvalidVersionUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	path := "/api/v1/workflows/" + testWFID.String() + "/versions/bad/clone"
	w := do(newRouter(h), adminReq(http.MethodPost, path, map[string]any{"new_key": "x", "new_name": "y"}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestPromoteVersion_MissingCtx(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/promote"
	w := do(newBareRouter(h), req(http.MethodPost, path, nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestExportBPMN_MissingCtx(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/export"
	w := do(newBareRouter(h), req(http.MethodGet, path, nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestExportBPMN_InvalidWorkflowUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	w := do(newRouter(h), req(http.MethodGet, "/api/v1/workflows/bad/versions/"+testVerID.String()+"/export", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetVersionDiff_MissingCtx(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	path := "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/diff/" + testVerID2.String()
	w := do(newBareRouter(h), req(http.MethodGet, path, nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetVersionDiff_InvalidWorkflowUUID(t *testing.T) {
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{}, &fakeValidationSvc{})
	path := "/api/v1/workflows/bad/versions/" + testVerID.String() + "/diff/" + testVerID2.String()
	w := do(newRouter(h), req(http.MethodGet, path, nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
