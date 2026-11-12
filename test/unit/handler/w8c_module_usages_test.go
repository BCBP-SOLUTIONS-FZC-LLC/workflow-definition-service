package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type w8cLogEntry struct {
	msg    string
	fields map[string]any
}

type w8cFieldLogger struct {
	mu     sync.Mutex
	errors []w8cLogEntry
}

func (*w8cFieldLogger) Debug(string, map[string]any) { /* no-op */ }
func (*w8cFieldLogger) Info(string, map[string]any)  { /* no-op */ }
func (*w8cFieldLogger) Warn(string, map[string]any)  { /* no-op */ }
func (*w8cFieldLogger) Fatal(string, map[string]any) { /* no-op */ }
func (l *w8cFieldLogger) Error(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, w8cLogEntry{msg: msg, fields: fields})
}

func w8cUsagesPath(id string) string { return "/api/v1/modules/" + id + "/usages" }

func TestListModuleUsages_NoIdentityInContext_Returns500InternalProblemBeforeAnyLookup(t *testing.T) {
	calls := 0
	h := newModuleHandler(&fakeModuleSvc{
		usages: func(context.Context, uuid.UUID, uuid.UUID) ([]*domain.ModuleUsage, error) {
			calls++
			return nil, nil
		},
	})
	path := w8cUsagesPath(testWFID.String())

	w := do(newBareRouter(h), req(http.MethodGet, path, nil))

	prob := w8cAssertProblem(t, w, http.StatusInternalServerError, w8cTypeInternal, "INTERNAL_ERROR", "missing request context", path)
	assert.Equal(t, "Internal Server Error", prob["title"])
	assert.Zero(t, calls, "without an identity the usages are never looked up")
}

func TestListModuleUsages_IDIsNotAUUID_Returns400BadRequestProblemBeforeAnyLookup(t *testing.T) {
	cases := []struct {
		name string
		id   string
	}{
		{"plain text", "not-a-uuid"},
		{"too short", "1234"},
		{"one character too many", testWFID.String() + "0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h := newModuleHandler(&fakeModuleSvc{
				usages: func(context.Context, uuid.UUID, uuid.UUID) ([]*domain.ModuleUsage, error) {
					calls++
					return nil, nil
				},
			})
			path := w8cUsagesPath(tc.id)

			w := do(newRouter(h), req(http.MethodGet, path, nil))

			w8cAssertProblem(t, w, http.StatusBadRequest, w8cTypeBadRequest, "BAD_REQUEST", "invalid id: not a valid UUID", path)
			assert.Zero(t, calls)
		})
	}
}

func TestListModuleUsages_UnclassifiedServiceError_Returns500GenericProblemAndLogsTheCause(t *testing.T) {
	var gotTenant, gotModule uuid.UUID
	log := &w8cFieldLogger{}
	h := handler.New(handler.Services{
		Modules: &fakeModuleSvc{
			usages: func(_ context.Context, tenantID, moduleID uuid.UUID) ([]*domain.ModuleUsage, error) {
				gotTenant, gotModule = tenantID, moduleID
				return nil, errors.New("list module usages: zzq_driver exploded at 10.0.0.7")
			},
		},
		Log: log,
	})
	path := w8cUsagesPath(testWFID.String())

	w := do(newRouter(h), req(http.MethodGet, path, nil))

	w8cAssertProblem(t, w, http.StatusInternalServerError, w8cTypeInternal, "INTERNAL_ERROR", "an unexpected error occurred", path)
	assert.NotContains(t, w.Body.String(), "zzq_driver")
	assert.NotContains(t, w.Body.String(), "10.0.0.7")
	assert.Equal(t, testTenantID, gotTenant)
	assert.Equal(t, testWFID, gotModule)
	require.Len(t, log.errors, 1)
	assert.Equal(t, "request failed", log.errors[0].msg)
	assert.Equal(t, 500, log.errors[0].fields["status"])
	assert.Equal(t, "INTERNAL_ERROR", log.errors[0].fields["code"])
	assert.Equal(t, testTenantID.String(), log.errors[0].fields["tenant_id"])
	assert.Equal(t, testUserID.String(), log.errors[0].fields["user_id"])
	assert.Equal(t, path, log.errors[0].fields["path"])
	assert.Contains(t, log.errors[0].fields["error"], "zzq_driver exploded")
}

func TestListModuleUsages_DraftReferenceHasNoVersionNumber_ReturnsNullVersionNumber(t *testing.T) {
	published := int32(4)
	draftWorkflow, publishedWorkflow := uuid.New(), uuid.New()
	draftVersion, publishedVersion := uuid.New(), uuid.New()
	var gotTenant, gotModule uuid.UUID
	h := newModuleHandler(&fakeModuleSvc{
		usages: func(_ context.Context, tenantID, moduleID uuid.UUID) ([]*domain.ModuleUsage, error) {
			gotTenant, gotModule = tenantID, moduleID
			return []*domain.ModuleUsage{
				{
					WorkflowID: draftWorkflow, WorkflowKey: "zzq-draft", WorkflowName: "Draft user", VersionID: draftVersion,
					VersionNumber: nil, Status: domain.VersionStatusDraft, ModuleVersionNumber: 1, Behind: false,
				},
				{
					WorkflowID: publishedWorkflow, WorkflowKey: "zzq-live", WorkflowName: "Live user", VersionID: publishedVersion,
					VersionNumber: &published, Status: domain.VersionStatusPublished, ModuleVersionNumber: 2, Behind: true,
				},
			}, nil
		},
	})

	w := do(newRouter(h), req(http.MethodGet, w8cUsagesPath(testWFID.String()), nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body struct {
		Usages []map[string]any `json:"usages"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Usages, 2)
	draft, live := body.Usages[0], body.Usages[1]
	assert.Contains(t, draft, "version_number", "the key is present, with a null value")
	assert.Nil(t, draft["version_number"])
	assert.Equal(t, draftWorkflow.String(), draft["workflow_id"])
	assert.Equal(t, draftVersion.String(), draft["version_id"])
	assert.Equal(t, "zzq-draft", draft["workflow_key"])
	assert.Equal(t, "Draft user", draft["workflow_name"])
	assert.Equal(t, "DRAFT", draft["status"])
	assert.Equal(t, float64(1), draft["module_version_number"])
	assert.Equal(t, false, draft["behind"])
	assert.Equal(t, float64(4), live["version_number"])
	assert.Equal(t, "PUBLISHED", live["status"])
	assert.Equal(t, true, live["behind"])
	assert.Equal(t, testTenantID, gotTenant)
	assert.Equal(t, testWFID, gotModule)
}
