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

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestArchiveModule_InvalidModuleID_Returns400(t *testing.T) {
	cases := []struct {
		name string
		id   string
	}{
		{name: "not a uuid", id: "not-a-uuid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var lookups, archives int
			h := newModuleHandler(&fakeModuleSvc{
				get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
					lookups++
					return nil, nil, nil
				},
				archive: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
					archives++
					return nil
				},
			})

			w := do(newRouter(h), adminReq(http.MethodDelete, "/api/v1/modules/"+tc.id, nil))

			require.Equal(t, http.StatusBadRequest, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, "invalid id: not a valid UUID", prob["detail"])
			assert.Zero(t, lookups)
			assert.Zero(t, archives)
		})
	}
}

func TestArchiveModule_ServiceFailure_MapsToProblem(t *testing.T) {
	cases := []struct {
		name       string
		getErr     error
		archiveErr error
		wantStatus int
		wantCode   string
		wantArchiv bool
	}{
		{
			name:       "module not found during the scope pre-check",
			getErr:     fmt.Errorf("get module: %w", domain.ErrNotFound),
			wantStatus: http.StatusNotFound,
			wantCode:   "NOT_FOUND",
		},
		{
			name:       "version or module removed while archiving",
			archiveErr: fmt.Errorf("archive module: %w", domain.ErrNotFound),
			wantStatus: http.StatusNotFound,
			wantCode:   "NOT_FOUND",
			wantArchiv: true,
		},
		{
			name:       "module vanishes between the pre-check and the transaction",
			archiveErr: fmt.Errorf("archive module: %w", fmt.Errorf("get module: %w", domain.ErrNotFound)),
			wantStatus: http.StatusNotFound,
			wantCode:   "NOT_FOUND",
			wantArchiv: true,
		},
		{
			name:       "database unavailable during the scope pre-check",
			getErr:     fmt.Errorf("get module: %w", fmt.Errorf("%w: connection refused", domain.ErrUpstreamUnavailable)),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
		},
		{
			name:       "database unavailable during the transaction",
			archiveErr: fmt.Errorf("archive module: %w", fmt.Errorf("%w: pool closed", domain.ErrUpstreamUnavailable)),
			wantStatus: http.StatusServiceUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			wantArchiv: true,
		},
		{
			name:       "unexpected database error during the scope pre-check",
			getErr:     fmt.Errorf("get module: %w", errors.New("scan failed")),
			wantStatus: http.StatusInternalServerError,
			wantCode:   "INTERNAL_ERROR",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var archives int
			var getTenant, getModule, archTenant, archUser, archModule uuid.UUID
			h := newModuleHandler(&fakeModuleSvc{
				get: func(_ context.Context, tenantID, moduleID uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
					getTenant, getModule = tenantID, moduleID
					if tc.getErr != nil {
						return nil, nil, tc.getErr
					}
					return &domain.Module{ID: testWFID, Scope: domain.ScopeTenant}, nil, nil
				},
				archive: func(_ context.Context, tenantID, userID, moduleID uuid.UUID) error {
					archives++
					archTenant, archUser, archModule = tenantID, userID, moduleID
					return tc.archiveErr
				},
			})

			w := do(newRouter(h), adminReq(http.MethodDelete, "/api/v1/modules/"+testWFID.String(), nil))

			require.Equal(t, tc.wantStatus, w.Code)
			assert.Equal(t, tc.wantCode, decodeProblem(t, w)["code"])
			assert.Equal(t, testTenantID, getTenant)
			assert.Equal(t, testWFID, getModule)
			if tc.wantArchiv {
				assert.Equal(t, 1, archives)
				assert.Equal(t, testTenantID, archTenant)
				assert.Equal(t, testUserID, archUser)
				assert.Equal(t, testWFID, archModule)
			} else {
				assert.Zero(t, archives)
			}
		})
	}
}

func TestArchiveModule_OK_ReturnsArchivedBody(t *testing.T) {
	cases := []struct {
		name string
	}{
		{name: "module with an active version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var gotTenant, gotUser, gotModule, getTenant, getModule uuid.UUID
			h := newModuleHandler(&fakeModuleSvc{
				get: func(_ context.Context, tenantID, moduleID uuid.UUID) (*domain.Module, *domain.ModuleVersion, error) {
					getTenant, getModule = tenantID, moduleID
					return &domain.Module{ID: testWFID, Scope: domain.ScopeTenant}, nil, nil
				},
				archive: func(_ context.Context, tenantID, userID, moduleID uuid.UUID) error {
					gotTenant, gotUser, gotModule = tenantID, userID, moduleID
					return nil
				},
			})

			w := do(newRouter(h), adminReq(http.MethodDelete, "/api/v1/modules/"+testWFID.String(), nil))

			require.Equal(t, http.StatusOK, w.Code)
			body := decodeProblem(t, w)
			assert.Equal(t, testWFID.String(), body["module_id"])
			assert.Equal(t, "ARCHIVED", body["status"])
			assert.Equal(t, "Module archived successfully", body["message"])
			assert.Equal(t, testTenantID, gotTenant)
			assert.Equal(t, testUserID, gotUser)
			assert.Equal(t, testWFID, gotModule)
			assert.Equal(t, testTenantID, getTenant)
			assert.Equal(t, testWFID, getModule)
		})
	}
}
