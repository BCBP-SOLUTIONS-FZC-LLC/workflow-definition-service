package handler_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func w9bContractPath() string {
	return "/api/v1/modules/" + testWFID.String() + "/versions/" + testVerID.String() + "/contract"
}

func w9bInvalidParams(t *testing.T, prob map[string]any) []map[string]any {
	t.Helper()
	return addModuleVersionWiringParams(t, prob)
}

func TestGetModuleContract_CallPinsAVersionThatIsNotPublished_Returns422NamingTheCall(t *testing.T) {
	t.Parallel()
	neverPublished := &domain.ModuleVersion{
		ID: uuid.New(), ModuleID: uuid.New(), ProcessID: "Process_Review", Status: domain.VersionStatusDraft, BPMNXML: "<draft/>",
	}
	cases := []struct {
		name   string
		lookup func() (*domain.ModuleVersion, error)
	}{
		{"no such version of the module", func() (*domain.ModuleVersion, error) { return nil, domain.ErrNotFound }},
		{"the version found was never published", func() (*domain.ModuleVersion, error) { return neverPublished, nil }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newModuleContractFailureWire(t, bpmn_compiler.New())
			w.storedVersion("Process_Outer", w9bCallerXML("Process_Outer", w9bCallSpec{id: "CA_Review", process: "Process_Review", pin: "99"}))
			w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Review", int32(99)).DoAndReturn(
				func(context.Context, uuid.UUID, string, int32) (*domain.ModuleVersion, error) { return tc.lookup() })

			rec := do(w.router, req(http.MethodGet, w9bContractPath(), nil))

			prob := w8cAssertProblem(t, rec, http.StatusUnprocessableEntity,
				"https://api.workflow.platform/errors/validation-failed", "BPMN_VALIDATION_FAILED",
				"BPMN semantic or structural validation failed.", w9bContractPath())
			params := w9bInvalidParams(t, prob)
			require.Len(t, params, 1)
			assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", params[0]["code"])
			assert.Equal(t, "CA_Review", params[0]["name"])
			assert.Contains(t, params[0]["reason"], `"Process_Review" version 99`)
		})
	}
}

func TestGetModuleContract_TwoCallsToTheSameMissingModule_Returns422WithOneEntryPerCall(t *testing.T) {
	t.Parallel()
	w := newModuleContractFailureWire(t, bpmn_compiler.New())
	w.storedVersion("Process_Outer", w9bCallerXML("Process_Outer",
		w9bCallSpec{id: "CA_First", process: "Process_Missing"},
		w9bCallSpec{id: "CA_Second", process: "Process_Missing"},
	))
	w.versions.EXPECT().Resolve(gomock.Any(), testTenantID, "Process_Missing", int32(0)).Return(nil, domain.ErrNotFound)

	rec := do(w.router, req(http.MethodGet, w9bContractPath(), nil))

	prob := w8cAssertProblem(t, rec, http.StatusUnprocessableEntity,
		"https://api.workflow.platform/errors/validation-failed", "BPMN_VALIDATION_FAILED",
		"BPMN semantic or structural validation failed.", w9bContractPath())
	params := w9bInvalidParams(t, prob)
	require.Len(t, params, 2, "the module is looked up once and every call that names it is reported")
	names := []any{params[0]["name"], params[1]["name"]}
	assert.ElementsMatch(t, []any{"CA_First", "CA_Second"}, names)
	for _, p := range params {
		assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", p["code"])
		assert.Contains(t, p["reason"], `"Process_Missing"`)
	}
}
