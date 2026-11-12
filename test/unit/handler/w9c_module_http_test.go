package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const (
	w9cTypeBadRequest = "https://api.workflow.platform/errors/bad-request"
	w9cTypeNotFound   = "https://api.workflow.platform/errors/not-found"
	w9cTypeConflict   = "https://api.workflow.platform/errors/conflict"
	w9cTypeValidation = "https://api.workflow.platform/errors/validation-failed"
	w9cTypeInternal   = "https://api.workflow.platform/errors/internal-error"

	w9cNSBPMN  = "http://www.omg.org/spec/BPMN/20100524/MODEL"
	w9cNSZeebe = "http://camunda.org/schema/zeebe/1.0"

	w9cDetailInternal = "an unexpected error occurred"
	w9cLeaked         = "pq: relation workflow_module_version is corrupt"
)

type w9cProblem struct {
	Type          string `json:"type"`
	Status        int    `json:"status"`
	Detail        string `json:"detail"`
	Instance      string `json:"instance"`
	Code          string `json:"code"`
	InvalidParams []struct {
		Name   string `json:"name"`
		Reason string `json:"reason"`
		Code   string `json:"code"`
	} `json:"invalid_params"`
}

func w9cDecode(t *testing.T, rec *httptest.ResponseRecorder) w9cProblem {
	t.Helper()
	var p w9cProblem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &p), rec.Body.String())
	return p
}

func w9cModuleXML(processID, callID, calledProcessID string) string {
	call := ""
	flows := `<bpmn:sequenceFlow id="Flow_1" sourceRef="Start_1" targetRef="End_1"/>`
	if calledProcessID != "" {
		call = fmt.Sprintf(`<bpmn:callActivity id=%q name="Call"><bpmn:extensionElements><zeebe:calledElement processId=%q/></bpmn:extensionElements></bpmn:callActivity>`,
			callID, calledProcessID)
		flows = fmt.Sprintf(`<bpmn:sequenceFlow id="Flow_1" sourceRef="Start_1" targetRef="%[1]s"/><bpmn:sequenceFlow id="Flow_2" sourceRef="%[1]s" targetRef="End_1"/>`, callID)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="%s" xmlns:zeebe="%s" id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="%s" name="Module" isExecutable="true">
    <bpmn:startEvent id="Start_1"/>%s<bpmn:endEvent id="End_1"/>%s
  </bpmn:process>
</bpmn:definitions>`, w9cNSBPMN, w9cNSZeebe, processID, call, flows)
}

func w9cAddVersionPath() string {
	return "/api/v1/modules/" + testWFID.String() + "/versions"
}

func w9cPublishPath() string {
	return "/api/v1/modules/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
}

func w9cArchivePath() string {
	return "/api/v1/modules/" + testWFID.String()
}

func w9cDraft(processID, bpmnXML string) *domain.ModuleVersion {
	return &domain.ModuleVersion{
		ID: testVerID, ModuleID: testWFID, TenantID: &testTenantID, Scope: domain.ScopeTenant,
		Status: domain.VersionStatusDraft, BPMNXML: bpmnXML, ProcessID: processID,
	}
}

func w9cPublishedVersion(processID string, number int32, bpmnXML string) *domain.ModuleVersion {
	n := number
	return &domain.ModuleVersion{
		ID: uuid.New(), ModuleID: uuid.New(), TenantID: &testTenantID, Scope: domain.ScopeTenant,
		Status: domain.VersionStatusPublished, BPMNXML: bpmnXML, ProcessID: processID, VersionNumber: &n,
	}
}

func TestAddModuleVersion_UnparseableXML_Returns400InvalidBpmnXmlAndStoresNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		xml  string
	}{
		{name: "a tag that is never closed", xml: `<bpmn:definitions xmlns:bpmn="` + w9cNSBPMN + `"><bpmn:process id="P1"`},
		{name: "end tag that closes another element", xml: `<a><b></a>`},
		{name: "an attribute without a value", xml: `<bpmn:definitions xmlns:bpmn="` + w9cNSBPMN + `" id>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := w8dNewModules(t, bpmn_compiler.New(), nil)
			w.expectModule(nil)

			rec := do(w.router, adminReq(http.MethodPost, w9cAddVersionPath(), map[string]any{"bpmn_xml": tc.xml}))

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			p := w9cDecode(t, rec)
			assert.Equal(t, w9cTypeBadRequest, p.Type)
			assert.Equal(t, http.StatusBadRequest, p.Status)
			assert.Equal(t, "INVALID_BPMN_XML", p.Code)
			assert.Equal(t, "the BPMN document could not be parsed", p.Detail)
			assert.Equal(t, w9cAddVersionPath(), p.Instance)
			assert.Empty(t, p.InvalidParams, "an unreadable document has no per-element findings")
			assert.Empty(t, w.log.infoLines("module version added"))
		})
	}
}

func TestAddModuleVersion_XMLWithoutADeclaredNamespace_Returns422MissingNamespaceAndStoresNothing(t *testing.T) {
	t.Parallel()
	withoutBPMN := fmt.Sprintf(`<definitions xmlns:zeebe=%q><process id="Process_W9c" isExecutable="true"><startEvent id="S"/></process></definitions>`, w9cNSZeebe)
	withoutZeebe := fmt.Sprintf(`<bpmn:definitions xmlns:bpmn=%q><bpmn:process id="Process_W9c" isExecutable="true"><bpmn:startEvent id="S"/></bpmn:process></bpmn:definitions>`, w9cNSBPMN)
	cases := []struct {
		name       string
		xml        string
		wantReason string
	}{
		{name: "BPMN namespace not declared", xml: withoutBPMN, wantReason: w9cNSBPMN},
		{name: "Zeebe namespace not declared", xml: withoutZeebe, wantReason: w9cNSZeebe},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := w8dNewModules(t, bpmn_compiler.New(), nil)
			w.expectModule(nil)

			rec := do(w.router, adminReq(http.MethodPost, w9cAddVersionPath(), map[string]any{"bpmn_xml": tc.xml}))

			require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			p := w9cDecode(t, rec)
			assert.Equal(t, w9cTypeValidation, p.Type)
			assert.Equal(t, http.StatusUnprocessableEntity, p.Status)
			assert.Equal(t, "BPMN_VALIDATION_FAILED", p.Code)
			assert.Equal(t, w9cAddVersionPath(), p.Instance)
			require.Len(t, p.InvalidParams, 1)
			assert.Equal(t, "MISSING_NAMESPACE", p.InvalidParams[0].Code)
			assert.Contains(t, p.InvalidParams[0].Reason, tc.wantReason)
			assert.Empty(t, w.log.infoLines("module version added"))
		})
	}
}

func TestAddModuleVersion_XMLWithNoProcessOrSeveral_Returns422NamingTheProcessCount(t *testing.T) {
	t.Parallel()
	prefix := fmt.Sprintf(`<bpmn:definitions xmlns:bpmn=%q xmlns:zeebe=%q id="D" targetNamespace="http://bpmn.io/schema/bpmn">`, w9cNSBPMN, w9cNSZeebe)
	process := func(id string) string {
		return fmt.Sprintf(`<bpmn:process id=%q isExecutable="true"><bpmn:startEvent id="S_%s"/><bpmn:endEvent id="E_%s"/><bpmn:sequenceFlow id="F_%s" sourceRef="S_%s" targetRef="E_%s"/></bpmn:process>`, id, id, id, id, id, id)
	}
	cases := []struct {
		name       string
		xml        string
		wantCode   string
		wantReason string
	}{
		{
			name:       "no process at all",
			xml:        prefix + `</bpmn:definitions>`,
			wantCode:   "NO_RUNNABLE_PROCESS",
			wantReason: "module BPMN must contain exactly one top-level process; got 0",
		},
		{
			name:       "two top-level processes",
			xml:        prefix + process("Process_W9cOne") + process("Process_W9cTwo") + `</bpmn:definitions>`,
			wantCode:   "MULTIPLE_PROCESSES",
			wantReason: "module BPMN must contain exactly one top-level process; got 2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := w8dNewModules(t, bpmn_compiler.New(), nil)
			w.expectModule(nil)

			rec := do(w.router, adminReq(http.MethodPost, w9cAddVersionPath(), map[string]any{"bpmn_xml": tc.xml}))

			require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
			p := w9cDecode(t, rec)
			assert.Equal(t, w9cTypeValidation, p.Type)
			assert.Equal(t, "BPMN_VALIDATION_FAILED", p.Code)
			assert.Equal(t, w9cAddVersionPath(), p.Instance)
			require.Len(t, p.InvalidParams, 1)
			assert.Equal(t, tc.wantCode, p.InvalidParams[0].Code)
			assert.Equal(t, tc.wantReason, p.InvalidParams[0].Reason)
			assert.Empty(t, w.log.infoLines("module version added"))
		})
	}
}

func TestPublishModuleVersion_DraftCallsAnUnresolvableModule_Returns422OnTheCallNodeAndPublishesNothing(t *testing.T) {
	t.Parallel()
	const (
		processID = "Process_W9cAlpha"
		missing   = "Process_W9cMissing"
	)
	w := w8dNewModules(t, bpmn_compiler.New(), nil)
	w.expectModule(nil)
	w.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).
		Return(w9cDraft(processID, w9cModuleXML(processID, "Alpha_Call", missing)), nil)
	w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, missing, int32(0)).Return(nil, domain.ErrNotFound)

	rec := do(w.router, adminReq(http.MethodPost, w9cPublishPath(), nil))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	p := w9cDecode(t, rec)
	assert.Equal(t, w9cTypeValidation, p.Type)
	assert.Equal(t, "BPMN_VALIDATION_FAILED", p.Code)
	assert.Equal(t, w9cPublishPath(), p.Instance)
	require.Len(t, p.InvalidParams, 1)
	assert.Equal(t, "Alpha_Call", p.InvalidParams[0].Name)
	assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", p.InvalidParams[0].Code)
	assert.Contains(t, p.InvalidParams[0].Reason, missing)
	assert.Zero(t, w.tx.runs.Load(), "a refused draft never opens the publish transaction")
	assert.Empty(t, w.log.infoLines("module version published"))
}

func TestPublishModuleVersion_ProblemInsideACalledModule_Returns422OnTheRootCallNode(t *testing.T) {
	t.Parallel()
	const (
		alpha   = "Process_W9cAlpha"
		beta    = "Process_W9cBeta"
		gamma   = "Process_W9cGamma"
		missing = "Process_W9cMissing"
	)
	w := w8dNewModules(t, bpmn_compiler.New(), nil)
	w.expectModule(nil)
	w.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).
		Return(w9cDraft(alpha, w9cModuleXML(alpha, "Alpha_Call", beta)), nil)
	w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, beta, int32(0)).
		Return(w9cPublishedVersion(beta, 1, w9cModuleXML(beta, "Beta_Call", gamma)), nil)
	w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, gamma, int32(0)).
		Return(w9cPublishedVersion(gamma, 1, w9cModuleXML(gamma, "Gamma_Call", missing)), nil)
	w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, missing, int32(0)).Return(nil, domain.ErrNotFound)

	rec := do(w.router, adminReq(http.MethodPost, w9cPublishPath(), nil))

	require.Equal(t, http.StatusUnprocessableEntity, rec.Code, rec.Body.String())
	p := w9cDecode(t, rec)
	assert.Equal(t, w9cTypeValidation, p.Type)
	assert.Equal(t, "BPMN_VALIDATION_FAILED", p.Code)
	assert.Equal(t, w9cPublishPath(), p.Instance)
	require.Len(t, p.InvalidParams, 1, "the problem is reported once, on the call of the module being published")
	assert.Equal(t, "Alpha_Call", p.InvalidParams[0].Name, "the finding points at the root call, not at Beta_Call or Gamma_Call")
	assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", p.InvalidParams[0].Code)
	assert.True(t, strings.HasPrefix(p.InvalidParams[0].Reason,
		`inside module "`+beta+`" version 1 → module "`+gamma+`" version 1: `), p.InvalidParams[0].Reason)
	assert.Contains(t, p.InvalidParams[0].Reason, missing)
	assert.Zero(t, w.tx.runs.Load())
	assert.Empty(t, w.log.infoLines("module version published"))
}

func TestPublishModuleVersion_ActiveVersionRowMissing_Returns404BeforePublishRuns(t *testing.T) {
	t.Parallel()
	activeID := uuid.New()
	w := w8dNewModules(t, nil, nil)
	w.expectModule(&activeID)
	w.versions.EXPECT().GetByID(gomock.Any(), testTenantID, activeID).Return(nil, domain.ErrNotFound)

	rec := do(w.router, adminReq(http.MethodPost, w9cPublishPath(), nil))

	require.Equal(t, http.StatusNotFound, rec.Code, rec.Body.String())
	p := w9cDecode(t, rec)
	assert.Equal(t, w9cTypeNotFound, p.Type)
	assert.Equal(t, http.StatusNotFound, p.Status)
	assert.Equal(t, "NOT_FOUND", p.Code)
	assert.Equal(t, "resource not found", p.Detail)
	assert.Equal(t, w9cPublishPath(), p.Instance)
	assert.Zero(t, w.tx.runs.Load(), "the module read fails before the publish transaction")
	assert.Empty(t, w.log.infoLines("module version published"))
}

func TestPublishModuleVersion_CompilerOrResolverFails_Returns500WithoutTheInternalText(t *testing.T) {
	t.Parallel()
	const processID = "Process_W9cAlpha"
	cases := []struct {
		name  string
		setup func(w *w8dModules)
	}{
		{
			name: "the compiler cannot list the module calls",
			setup: func(w *w8dModules) {
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<draft/>").Return(nil, nil, errors.New(w9cLeaked))
			},
		},
		{
			name: "the lookup of a called module fails with an unclassified error",
			setup: func(w *w8dModules) {
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<draft/>").
					Return([]domain.ModuleCall{{CallID: "Call_1", ProcessID: "Process_Called"}}, nil, nil)
				w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Called", int32(0)).Return(nil, errors.New(w9cLeaked))
			},
		},
		{
			name: "the compiler cannot bundle the modules",
			setup: func(w *w8dModules) {
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<draft/>").Return(nil, nil, nil)
				w.compiler.EXPECT().BundleModules("<draft/>", gomock.Any()).Return("", errors.New(w9cLeaked))
			},
		},
		{
			name: "the compiler cannot validate the bundle",
			setup: func(w *w8dModules) {
				w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<draft/>").Return(nil, nil, nil)
				w.compiler.EXPECT().BundleModules("<draft/>", gomock.Any()).Return("<bundled/>", nil)
				w.compiler.EXPECT().ValidateModule(gomock.Any(), "<bundled/>", processID, false).Return(nil, errors.New(w9cLeaked))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := w8dNewModules(t, nil, nil)
			w.expectModule(nil)
			w.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(w9cDraft(processID, "<draft/>"), nil)
			tc.setup(w)

			rec := do(w.router, adminReq(http.MethodPost, w9cPublishPath(), nil))

			require.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
			p := w9cDecode(t, rec)
			assert.Equal(t, w9cTypeInternal, p.Type)
			assert.Equal(t, http.StatusInternalServerError, p.Status)
			assert.Equal(t, "INTERNAL_ERROR", p.Code)
			assert.Equal(t, w9cDetailInternal, p.Detail)
			assert.Equal(t, w9cPublishPath(), p.Instance)
			assert.NotContains(t, rec.Body.String(), w9cLeaked)
			assert.Zero(t, w.tx.runs.Load(), "a failed check never opens the publish transaction")
			assert.Empty(t, w.log.infoLines("module version published"))
			logged := w.log.errorLines("request failed")
			require.Len(t, logged, 1)
			assert.EqualValues(t, http.StatusInternalServerError, logged[0].fields["status"])
		})
	}
}

func TestPublishModuleVersion_ActivePointerUpdateFails_MapsTheCauseAndReportsNoSuccess(t *testing.T) {
	t.Parallel()
	const processID = "Process_W9cAlpha"
	cases := []struct {
		name       string
		cause      error
		wantStatus int
		wantType   string
		wantCode   string
		wantDetail string
	}{
		{
			name:       "the module is gone",
			cause:      fmt.Errorf("update module pointer: %w", domain.ErrNotFound),
			wantStatus: http.StatusNotFound,
			wantType:   w9cTypeNotFound,
			wantCode:   "NOT_FOUND",
			wantDetail: "resource not found",
		},
		{
			name:       "the version is not published",
			cause:      domain.ErrVersionNotPublished,
			wantStatus: http.StatusConflict,
			wantType:   w9cTypeConflict,
			wantCode:   "INVALID_VERSION_STATUS",
			wantDetail: "workflow version is not in PUBLISHED status",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := w8dNewModules(t, nil, nil)
			w.expectModule(nil)
			w.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(w9cDraft(processID, "<draft/>"), nil)
			w.compiler.EXPECT().ModuleCalls(gomock.Any(), "<draft/>").Return(nil, nil, nil)
			w.compiler.EXPECT().BundleModules("<draft/>", gomock.Any()).Return("<bundled/>", nil)
			w.compiler.EXPECT().ValidateModule(gomock.Any(), "<bundled/>", processID, false).Return(nil, nil)
			next := w.versions.EXPECT().NextVersionNumber(gomock.Any(), testWFID).Return(int32(3), nil)
			publish := w.versions.EXPECT().Publish(gomock.Any(), testTenantID, testVerID, int32(3)).Return(nil).After(next)
			var pointerInTx bool
			w.modules.EXPECT().UpdateActiveVersion(gomock.Any(), testWFID, gomock.Any()).DoAndReturn(
				func(_ context.Context, _ uuid.UUID, versionID *uuid.UUID) error {
					pointerInTx = w.tx.inside()
					require.NotNil(t, versionID)
					assert.Equal(t, testVerID, *versionID)
					return tc.cause
				}).After(publish)

			rec := do(w.router, adminReq(http.MethodPost, w9cPublishPath(), nil))

			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			p := w9cDecode(t, rec)
			assert.Equal(t, tc.wantType, p.Type)
			assert.Equal(t, tc.wantStatus, p.Status)
			assert.Equal(t, tc.wantCode, p.Code)
			assert.Equal(t, tc.wantDetail, p.Detail)
			assert.Equal(t, w9cPublishPath(), p.Instance)
			assert.True(t, pointerInTx, "the pointer update runs inside the transaction that published the version")
			assert.Equal(t, int32(1), w.tx.runs.Load())
			assert.NotContains(t, rec.Body.String(), "version_number")
			assert.Empty(t, w.log.infoLines("module version published"))
		})
	}
}

func TestPublishModuleVersion_ArchivedVersion_Returns409AndChangesNothing(t *testing.T) {
	t.Parallel()
	w := w8dNewModules(t, nil, nil)
	w.expectModule(nil)
	archived := w9cDraft("Process_W9cAlpha", "<archived/>")
	archived.Status = domain.VersionStatusArchived
	n := int32(2)
	archived.VersionNumber = &n
	w.versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(archived, nil)

	rec := do(w.router, adminReq(http.MethodPost, w9cPublishPath(), nil))

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	p := w9cDecode(t, rec)
	assert.Equal(t, w9cTypeConflict, p.Type)
	assert.Equal(t, http.StatusConflict, p.Status)
	assert.Equal(t, "INVALID_VERSION_STATUS", p.Code)
	assert.Equal(t, "invalid version status for this operation", p.Detail)
	assert.Equal(t, w9cPublishPath(), p.Instance)
	assert.Zero(t, w.tx.runs.Load(), "an archived version is refused before any transaction")
	assert.Equal(t, domain.VersionStatusArchived, archived.Status)
	assert.Empty(t, w.log.infoLines("module version published"))
}

func TestArchiveModule_ActiveVersionClearedBeforeTheTransactionRead_Returns409NoActiveVersion(t *testing.T) {
	t.Parallel()
	activeID := uuid.New()
	w := w8dNewModules(t, nil, nil)
	reads := 0
	var lastReadInTx bool
	w.modules.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).DoAndReturn(
		func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, error) {
			reads++
			lastReadInTx = w.tx.inside()
			m := &domain.Module{
				ID: testWFID, TenantID: &testTenantID, Scope: domain.ScopeTenant, ProcessID: "Process_W9cAlpha",
			}
			if reads < 3 {
				m.ActiveVersionID = &activeID
			}
			return m, nil
		}).Times(3)
	w.versions.EXPECT().GetByID(gomock.Any(), testTenantID, activeID).
		Return(&domain.ModuleVersion{ID: activeID, ModuleID: testWFID}, nil).AnyTimes()

	rec := do(w.router, adminReq(http.MethodDelete, w9cArchivePath(), nil))

	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	p := w9cDecode(t, rec)
	assert.Equal(t, w9cTypeConflict, p.Type)
	assert.Equal(t, http.StatusConflict, p.Status)
	assert.Equal(t, "NO_ACTIVE_VERSION", p.Code)
	assert.Equal(t, "no active version", p.Detail)
	assert.Equal(t, w9cArchivePath(), p.Instance)
	assert.Equal(t, 3, reads, "scope check, the service's first read and the read inside the transaction")
	assert.True(t, lastReadInTx, "the read that finds the pointer cleared is the one inside the transaction")
	assert.Equal(t, int32(1), w.tx.runs.Load())
	assert.Empty(t, w.log.infoLines("module archived"))
}
