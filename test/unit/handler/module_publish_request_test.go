package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func modulePublishRequestPath(moduleID, versionID string) string {
	return "/api/v1/modules/" + moduleID + "/versions/" + versionID + "/publish"
}

func TestPublishModuleVersion_MalformedPathIDs_Returns400WithoutLookup(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		moduleID   string
		versionID  string
		wantDetail string
	}{
		{"module id is not a UUID", "not-a-uuid", testVerID.String(), "invalid id: not a valid UUID"},
		{"version id is not a UUID", testWFID.String(), "not-a-uuid", "invalid version_id: not a valid UUID"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var lookups, publishes int
			h := newModuleHandler(&fakeModuleSvc{
				get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
					lookups++
					return &domain.Module{ID: testWFID, Scope: domain.ScopeTenant}, nil, nil
				},
				publish: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.ModuleVersion, error) {
					publishes++
					return nil, nil
				},
			})

			w := do(newRouter(h), adminReq(http.MethodPost, modulePublishRequestPath(tc.moduleID, tc.versionID), nil))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, tc.wantDetail, prob["detail"])
			assert.Zero(t, lookups, "a malformed id must not reach the module lookup")
			assert.Zero(t, publishes, "a malformed id must not reach publish")
		})
	}
}

func TestPublishModuleVersion_StoreUnavailable_Returns503(t *testing.T) {
	t.Parallel()

	unavailable := func(op string) error {
		return fmt.Errorf("%s: %w", op, fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable))
	}
	cases := []struct {
		name             string
		getErr           error
		publishErr       error
		wantPublishCalls int
	}{
		{name: "module lookup", getErr: unavailable("get module")},
		{name: "version lookup", publishErr: unavailable("get module version"), wantPublishCalls: 1},
		{name: "publish transaction", publishErr: unavailable("publish module version"), wantPublishCalls: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var publishCalls int
			var gotTenant, gotUser, gotModule, gotVersion uuid.UUID
			var getTenant, getModule uuid.UUID
			h := newModuleHandler(&fakeModuleSvc{
				get: func(_ context.Context, tenantID, moduleID uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
					getTenant, getModule = tenantID, moduleID
					if tc.getErr != nil {
						return nil, nil, tc.getErr
					}
					return &domain.Module{ID: testWFID, Scope: domain.ScopeTenant}, nil, nil
				},
				publish: func(_ context.Context, tenantID, userID, moduleID, versionID uuid.UUID) (*domain.ModuleVersion, error) {
					publishCalls++
					gotTenant, gotUser, gotModule, gotVersion = tenantID, userID, moduleID, versionID
					return nil, tc.publishErr
				},
			})

			w := do(newRouter(h), adminReq(http.MethodPost, modulePublishRequestPath(testWFID.String(), testVerID.String()), nil))

			require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, "UPSTREAM_UNAVAILABLE", prob["code"])
			assert.Equal(t, "an upstream dependency is temporarily unavailable", prob["detail"])
			assert.Equal(t, tc.wantPublishCalls, publishCalls)
			assert.Equal(t, testTenantID, getTenant)
			assert.Equal(t, testWFID, getModule)
			if tc.wantPublishCalls > 0 {
				assert.Equal(t, testTenantID, gotTenant)
				assert.Equal(t, testUserID, gotUser)
				assert.Equal(t, testWFID, gotModule)
				assert.Equal(t, testVerID, gotVersion)
			}
		})
	}
}

func TestPublishModuleVersion_ValidationFindings_Returns422WithOneEntryPerFinding(t *testing.T) {
	t.Parallel()

	findings := []domain.BPMNValidationError{
		{Code: "CALLED_ELEMENT_CYCLE", NodeID: "Call_1", Message: "call closes a cycle", Severity: domain.SeverityError},
		{Code: "MISSING_END_EVENT", NodeID: "Process_1", Message: "process has no end event", Severity: domain.SeverityError},
	}
	var getTenant, getModule, pubTenant, pubUser, pubModule, pubVersion uuid.UUID
	h := newModuleHandler(&fakeModuleSvc{
		get: func(_ context.Context, tenantID, moduleID uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
			getTenant, getModule = tenantID, moduleID
			return &domain.Module{ID: testWFID, Scope: domain.ScopeTenant}, nil, nil
		},
		publish: func(_ context.Context, tenantID, userID, moduleID, versionID uuid.UUID) (*domain.ModuleVersion, error) {
			pubTenant, pubUser, pubModule, pubVersion = tenantID, userID, moduleID, versionID
			return nil, &domain.ValidationFailedError{Errors: findings}
		},
	})

	w := do(newRouter(h), adminReq(http.MethodPost, modulePublishRequestPath(testWFID.String(), testVerID.String()), nil))

	require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	assert.Equal(t, testTenantID, getTenant)
	assert.Equal(t, testWFID, getModule)
	assert.Equal(t, testTenantID, pubTenant)
	assert.Equal(t, testUserID, pubUser)
	assert.Equal(t, testWFID, pubModule)
	assert.Equal(t, testVerID, pubVersion)
	prob := decodeProblem(t, w)
	assert.Equal(t, "BPMN_VALIDATION_FAILED", prob["code"])
	params, ok := prob["invalid_params"].([]any)
	require.True(t, ok, "problem body carries invalid_params: %v", prob)
	require.Len(t, params, len(findings))
	for i, f := range findings {
		entry, _ := params[i].(map[string]any)
		assert.Equal(t, f.NodeID, entry["name"])
		assert.Equal(t, f.Message, entry["reason"])
		assert.Equal(t, string(f.Code), entry["code"])
	}
}
