package handler_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const w9bTypeNotFound = "https://api.workflow.platform/errors/not-found"

func w9bAddVersionBody(xml string) map[string]any {
	if xml == "" {
		return map[string]any{}
	}
	return map[string]any{"bpmn_xml": xml}
}

func TestAddModuleVersion_ExplicitXml_Returns201WithTheDraftBodyAndStoresTheCaller(t *testing.T) {
	t.Parallel()
	activeID := uuid.New()
	w := w8dNewModules(t, nil, nil)
	w.expectModule(&activeID)
	w.versions.EXPECT().GetByID(gomock.Any(), testTenantID, activeID).Return(&domain.ModuleVersion{
		ID: activeID, ModuleID: testWFID, BPMNXML: "<active/>", ProcessID: w8dProcessID,
	}, nil).Times(1)
	w.expectCleanCompile("<explicit/>")
	var stored *domain.ModuleVersion
	w.versions.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, v *domain.ModuleVersion) error {
			stored = v
			return nil
		})

	rec := do(w.router, adminReq(http.MethodPost, w8dAddVersionPath(), w9bAddVersionBody("<explicit/>")))

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.NotNil(t, stored)
	assert.Equal(t, "<explicit/>", stored.BPMNXML, "the XML sent is stored, not the active version's")
	assert.Equal(t, domain.VersionStatusDraft, stored.Status)
	assert.Equal(t, w8dProcessID, stored.ProcessID)
	assert.True(t, stored.IsValid)
	assert.Equal(t, testWFID, stored.ModuleID)
	assert.Equal(t, domain.ScopeTenant, stored.Scope)
	require.NotNil(t, stored.TenantID)
	assert.Equal(t, testTenantID, *stored.TenantID)
	require.NotNil(t, stored.CreatedByUserID)
	assert.Equal(t, testUserID, *stored.CreatedByUserID)
	assert.Equal(t, map[string]any{
		"module_id":  testWFID.String(),
		"version_id": stored.ID.String(),
		"status":     "DRAFT",
		"message":    "Module version added",
	}, w8dBody(t, rec))
	added := w.log.infoLines("module version added")
	require.Len(t, added, 1)
	assert.Equal(t, testUserID.String(), added[0].fields["user_id"])
	assert.Equal(t, stored.ID.String(), added[0].fields["version_id"])
}

func TestAddModuleVersion_ValidationFindings_Returns422ListingOnlyTheBlockingOnes(t *testing.T) {
	t.Parallel()
	w := w8dNewModules(t, nil, nil)
	w.expectModule(nil)
	w.compiler.EXPECT().ProcessID(gomock.Any(), "<bad/>").Return(w8dProcessID, nil)
	w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<bad/>").Return(nil, nil, nil)
	w.compiler.EXPECT().BundleModules("<bad/>", []domain.ResolvedModule(nil)).Return("<bundled/>", nil)
	w.compiler.EXPECT().ValidateModule(gomock.Any(), "<bundled/>", w8dProcessID, false).Return([]domain.BPMNValidationError{
		{Code: domain.BPMNErrNoStartEvent, NodeID: "Process_1", Message: "zzq no start", Severity: domain.SeverityError},
		{Code: domain.BPMNWarnUnknownStageType, NodeID: "Task_1", Message: "zzq advisory", Severity: domain.SeverityWarning},
		{Code: domain.BPMNErrUnmatchedGateway, NodeID: "GW_1", Message: "zzq gateway", Severity: domain.SeverityError},
	}, nil)

	rec := do(w.router, adminReq(http.MethodPost, w8dAddVersionPath(), w9bAddVersionBody("<bad/>")))

	prob := w8cAssertProblem(t, rec, http.StatusUnprocessableEntity,
		"https://api.workflow.platform/errors/validation-failed", "BPMN_VALIDATION_FAILED",
		"BPMN semantic or structural validation failed.", w8dAddVersionPath())
	params := addModuleVersionWiringParams(t, prob)
	require.Len(t, params, 2, "the warning is not listed")
	assert.Equal(t, map[string]any{"name": "Process_1", "reason": "zzq no start", "code": string(domain.BPMNErrNoStartEvent)}, params[0])
	assert.Equal(t, map[string]any{"name": "GW_1", "reason": "zzq gateway", "code": string(domain.BPMNErrUnmatchedGateway)}, params[1])
	assert.Empty(t, w.log.errorLines("request failed"), "findings are not a server failure")
	assert.Empty(t, w.log.infoLines("module version added"))
}

func TestAddModuleVersion_ActiveVersionRowIsMissing_Returns404AndAddsNothing(t *testing.T) {
	t.Parallel()
	activeID := uuid.New()
	w := w8dNewModules(t, nil, nil)
	w.expectModule(&activeID)
	w.versions.EXPECT().GetByID(gomock.Any(), testTenantID, activeID).Return(nil, domain.ErrNotFound).Times(1)

	rec := do(w.router, adminReq(http.MethodPost, w8dAddVersionPath(), w9bAddVersionBody("<explicit/>")))

	w8cAssertProblem(t, rec, http.StatusNotFound, w9bTypeNotFound, "NOT_FOUND", "resource not found", w8dAddVersionPath())
	assert.Empty(t, w.log.errorLines("request failed"), "a missing record is not a server failure")
	assert.Empty(t, w.log.infoLines("module version added"))
}

func TestAddModuleVersion_InsertFailsUnexpectedly_Returns500WithoutLeakingTheCause(t *testing.T) {
	t.Parallel()
	const leaked = "zzq_insert_exploded"
	w := w8dNewModules(t, nil, nil)
	w.expectModule(nil)
	w.expectCleanCompile("<x/>")
	w.versions.EXPECT().Create(gomock.Any(), gomock.Any()).Return(errors.New(leaked))

	rec := do(w.router, adminReq(http.MethodPost, w8dAddVersionPath(), w9bAddVersionBody("<x/>")))

	w8cAssertProblem(t, rec, http.StatusInternalServerError, w9bTypeInternal, "INTERNAL_ERROR", w8dDetailInternal, w8dAddVersionPath())
	assert.NotContains(t, rec.Body.String(), leaked)
	logged := w.log.errorLines("request failed")
	require.Len(t, logged, 1)
	assert.EqualValues(t, http.StatusInternalServerError, logged[0].fields["status"])
	assert.Equal(t, testTenantID.String(), logged[0].fields["tenant_id"])
	assert.Equal(t, testUserID.String(), logged[0].fields["user_id"])
	assert.Contains(t, logged[0].fields["error"], leaked)
	assert.Empty(t, w.log.infoLines("module version added"), "no success is logged for a failed insert")
}

func TestAddModuleVersion_SecondActiveVersionReadFails_ReturnsTheMappedProblem(t *testing.T) {
	t.Parallel()
	const leaked = "zzq_read_exploded"
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantType   string
		wantCode   string
		wantDetail string
		wantLogged int
	}{
		{"an unclassified error", errors.New(leaked), http.StatusInternalServerError, w9bTypeInternal, "INTERNAL_ERROR", w8dDetailInternal, 1},
		{"a missing row", domain.ErrNotFound, http.StatusNotFound, w9bTypeNotFound, "NOT_FOUND", "resource not found", 0},
		{"an unreachable database", fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable),
			http.StatusServiceUnavailable, w9bTypeUnavail, "UPSTREAM_UNAVAILABLE", w8dDetailUnavail, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			activeID := uuid.New()
			w := w8dNewModules(t, nil, nil)
			w.expectModule(&activeID)
			gomock.InOrder(
				w.versions.EXPECT().GetByID(gomock.Any(), testTenantID, activeID).Return(&domain.ModuleVersion{
					ID: activeID, ModuleID: testWFID, BPMNXML: "<active/>", ProcessID: w8dProcessID,
				}, nil).Times(1),
				w.versions.EXPECT().GetByID(gomock.Any(), testTenantID, activeID).Return(nil, tc.err).Times(1),
			)

			rec := do(w.router, adminReq(http.MethodPost, w8dAddVersionPath(), w9bAddVersionBody("")))

			w8cAssertProblem(t, rec, tc.wantStatus, tc.wantType, tc.wantCode, tc.wantDetail, w8dAddVersionPath())
			assert.NotContains(t, rec.Body.String(), leaked)
			assert.NotContains(t, rec.Body.String(), "connection refused")
			assert.Len(t, w.log.errorLines("request failed"), tc.wantLogged)
			assert.Empty(t, w.log.infoLines("module version added"))
		})
	}
}

func TestAddModuleVersion_ModuleValidatorFails_Returns500WithoutLeakingTheCause(t *testing.T) {
	t.Parallel()
	const leaked = "zzq_validator_crashed"
	w := w8dNewModules(t, nil, nil)
	w.expectModule(nil)
	w.compiler.EXPECT().ProcessID(gomock.Any(), "<x/>").Return(w8dProcessID, nil)
	w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<x/>").Return(nil, nil, nil)
	w.compiler.EXPECT().BundleModules("<x/>", []domain.ResolvedModule(nil)).Return("<bundled/>", nil)
	w.compiler.EXPECT().ValidateModule(gomock.Any(), "<bundled/>", w8dProcessID, false).Return(nil, errors.New(leaked))

	rec := do(w.router, adminReq(http.MethodPost, w8dAddVersionPath(), w9bAddVersionBody("<x/>")))

	w8cAssertProblem(t, rec, http.StatusInternalServerError, w9bTypeInternal, "INTERNAL_ERROR", w8dDetailInternal, w8dAddVersionPath())
	assert.NotContains(t, rec.Body.String(), leaked)
	logged := w.log.errorLines("request failed")
	require.Len(t, logged, 1)
	assert.EqualValues(t, http.StatusInternalServerError, logged[0].fields["status"])
	assert.Equal(t, "INTERNAL_ERROR", logged[0].fields["code"])
	assert.Contains(t, logged[0].fields["error"], leaked)
	assert.Empty(t, w.log.infoLines("module version added"))
}
