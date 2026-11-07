package handler_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type sgDModuleGetRig struct {
	modules  *mocks.MockModuleRepository
	versions *mocks.MockModuleVersionRepository
	router   *gin.Engine
	log      *sgDErrorLog
}

func sgDNewModuleGetRig(t *testing.T) *sgDModuleGetRig {
	t.Helper()
	ctrl := gomock.NewController(t)
	rig := &sgDModuleGetRig{
		modules:  mocks.NewMockModuleRepository(ctrl),
		versions: mocks.NewMockModuleVersionRepository(ctrl),
		log:      &sgDErrorLog{},
	}
	svc := service.NewModuleService(service.ModuleDeps{Modules: rig.modules, Versions: rig.versions})
	rig.router = newRouter(handler.New(handler.Services{Modules: svc, Log: rig.log}))
	return rig
}

func (r *sgDModuleGetRig) get(path string) (int, map[string]any, string) {
	w := do(r.router, req(http.MethodGet, path, nil))
	var body map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body, w.Body.String()
}

func TestGetModule_PathIDIsNotAUUID_Returns400BadRequestWithoutALookup(t *testing.T) {
	t.Parallel()

	rig := sgDNewModuleGetRig(t)

	code, prob, raw := rig.get("/api/v1/modules/not-a-uuid")

	require.Equal(t, http.StatusBadRequest, code, raw)
	assert.Equal(t, "BAD_REQUEST", prob["code"])
	assert.Equal(t, "invalid id: not a valid UUID", prob["detail"])
	assert.Equal(t, "https://api.workflow.platform/errors/bad-request", prob["type"])
	assert.Equal(t, "/api/v1/modules/not-a-uuid", prob["instance"])
	assert.NotContains(t, prob, "invalid_params")
	assert.NotContains(t, prob, "errors")
}

func TestGetModule_DatabaseFailures_MapToTheirProblems(t *testing.T) {
	t.Parallel()

	moduleID := uuid.New()
	activeID := uuid.New()
	cases := []struct {
		name         string
		moduleErr    error
		versionErr   error
		wantStatus   int
		wantCode     string
		wantDetail   string
		bodyMustLack []string
	}{
		{
			name:         "module query hits an outage",
			moduleErr:    fmt.Errorf("get module: %w", domain.ErrUpstreamUnavailable),
			wantStatus:   http.StatusServiceUnavailable,
			wantCode:     "UPSTREAM_UNAVAILABLE",
			wantDetail:   "an upstream dependency is temporarily unavailable",
			bodyMustLack: []string{"get module"},
		},
		{
			name:         "module query fails with an unclassified error",
			moduleErr:    errors.New("pq: deadlock detected on workflow_module"),
			wantStatus:   http.StatusInternalServerError,
			wantCode:     "INTERNAL_ERROR",
			wantDetail:   "an unexpected error occurred",
			bodyMustLack: []string{"deadlock", "workflow_module", "pq:"},
		},
		{
			name:         "active version query hits an outage after the module loaded",
			versionErr:   fmt.Errorf("lookup: %w", domain.ErrUpstreamUnavailable),
			wantStatus:   http.StatusServiceUnavailable,
			wantCode:     "UPSTREAM_UNAVAILABLE",
			wantDetail:   "an upstream dependency is temporarily unavailable",
			bodyMustLack: []string{"get active module version", "lookup", "process_id", "active_version", "\"name\""},
		},
		{
			name:         "active version query fails with an unclassified error",
			versionErr:   errors.New("pq: could not serialize access on workflow_module_version"),
			wantStatus:   http.StatusInternalServerError,
			wantCode:     "INTERNAL_ERROR",
			wantDetail:   "an unexpected error occurred",
			bodyMustLack: []string{"serialize", "workflow_module_version", "process_id", "active_version"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rig := sgDNewModuleGetRig(t)
			if tc.moduleErr != nil {
				rig.modules.EXPECT().GetByID(gomock.Any(), testTenantID, moduleID).Return(nil, tc.moduleErr)
			} else {
				rig.modules.EXPECT().GetByID(gomock.Any(), testTenantID, moduleID).
					Return(&domain.Module{ID: moduleID, Name: "Hidden Name", ProcessID: "Process_Hidden", Scope: domain.ScopeGlobal, ActiveVersionID: &activeID}, nil)
				rig.versions.EXPECT().GetByID(gomock.Any(), testTenantID, activeID).Return(nil, tc.versionErr)
			}

			code, prob, raw := rig.get("/api/v1/modules/" + moduleID.String())

			require.Equal(t, tc.wantStatus, code, raw)
			assert.Equal(t, tc.wantCode, prob["code"])
			assert.Equal(t, tc.wantDetail, prob["detail"])
			assert.Equal(t, "/api/v1/modules/"+moduleID.String(), prob["instance"])
			assert.NotContains(t, raw, "Hidden Name")
			for _, absent := range tc.bodyMustLack {
				assert.False(t, strings.Contains(raw, absent), "body leaks %q: %s", absent, raw)
			}
			entries := rig.log.snapshot()
			require.Len(t, entries, 1)
			assert.Equal(t, "request failed", entries[0].msg)
			assert.Equal(t, float64(tc.wantStatus), float64(entries[0].fields["status"].(int)))
			assert.Equal(t, testTenantID.String(), entries[0].fields["tenant_id"])
			assert.Equal(t, testUserID.String(), entries[0].fields["user_id"])
			assert.Contains(t, entries[0].fields, "trace_id")
		})
	}
}
