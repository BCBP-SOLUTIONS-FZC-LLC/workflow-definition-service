package handler_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type w6cWarning struct {
	msg    string
	fields map[string]any
}

type w6cWarnLog struct {
	mu    sync.Mutex
	warns []w6cWarning
}

func (l *w6cWarnLog) Debug(string, map[string]any) {}
func (l *w6cWarnLog) Info(string, map[string]any)  {}
func (l *w6cWarnLog) Error(string, map[string]any) {}
func (l *w6cWarnLog) Fatal(string, map[string]any) {}
func (l *w6cWarnLog) Warn(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, w6cWarning{msg: msg, fields: fields})
}

func (l *w6cWarnLog) withPrefix(prefix string) []w6cWarning {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []w6cWarning
	for _, w := range l.warns {
		if strings.HasPrefix(w.msg, prefix) {
			out = append(out, w)
		}
	}
	return out
}

func w6cPublishPath() string {
	return "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/publish"
}

func TestPublishVersion_IdempotencyCacheUnavailable_StillPublishesOnceAndAnswers200(t *testing.T) {
	t.Parallel()

	const rawKey = "w6c-client-key-publish"
	cases := []struct {
		name             string
		reserveFails     bool
		readFails        bool
		wantReserveWarns int
		wantReadWarns    int
		wantReleases     int
	}{
		{name: "reservation store fails", reserveFails: true, wantReserveWarns: 1, wantReadWarns: 0, wantReleases: 0},
		{name: "stored response read fails", readFails: true, wantReserveWarns: 0, wantReadWarns: 2, wantReleases: 1},
		{name: "reservation and read both fail", reserveFails: true, readFails: true, wantReserveWarns: 1, wantReadWarns: 2, wantReleases: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var mu sync.Mutex
			var setnxKeys, setKeys []string
			var gets, releases int
			cache := &fakeCache{
				get: func(context.Context, string) (string, error) {
					mu.Lock()
					defer mu.Unlock()
					gets++
					if tc.readFails {
						return "", errors.New("w6c cache read refused")
					}
					return "", nil
				},
				setnx: func(_ context.Context, key, _ string, _ time.Duration) (bool, error) {
					mu.Lock()
					defer mu.Unlock()
					setnxKeys = append(setnxKeys, key)
					if tc.reserveFails {
						return false, errors.New("w6c cache reservation refused")
					}
					return true, nil
				},
				set: func(_ context.Context, key, _ string, _ time.Duration) error {
					mu.Lock()
					defer mu.Unlock()
					setKeys = append(setKeys, key)
					return nil
				},
				delIfEqual: func(context.Context, string, string) error {
					mu.Lock()
					defer mu.Unlock()
					releases++
					return nil
				},
			}
			log := &w6cWarnLog{}

			published := newPublishedVersion()
			var publishCalls int
			var gotTenant, gotUser, gotWorkflow, gotVersion uuid.UUID
			var gotForce bool
			versions := &fakeVersionSvc{
				publish: func(_ context.Context, tenantID, userID, workflowID, versionID uuid.UUID, force bool) (*domain.WorkflowVersion, error) {
					publishCalls++
					gotTenant, gotUser, gotWorkflow, gotVersion, gotForce = tenantID, userID, workflowID, versionID, force
					return published, nil
				},
			}
			h := handler.New(handler.Services{Versions: versions, Cache: cache, Log: log, IdempotencyTTL: time.Hour})
			r := adminReq(http.MethodPost, w6cPublishPath(), nil)
			r.Header.Set("Idempotency-Key", rawKey)

			w := do(newRouter(h), r)

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.Equal(t, testWFID.String(), body["workflow_id"])
			assert.Equal(t, testVerID.String(), body["version_id"])
			assert.Equal(t, "PUBLISHED", body["status"])
			assert.Equal(t, float64(1), body["version_number"])
			assert.Equal(t, "Version published", body["message"])

			assert.Equal(t, 1, publishCalls, "the publish runs exactly once without idempotency protection")
			assert.Equal(t, testTenantID, gotTenant)
			assert.Equal(t, testUserID, gotUser)
			assert.Equal(t, testWFID, gotWorkflow)
			assert.Equal(t, testVerID, gotVersion)
			assert.False(t, gotForce)

			mu.Lock()
			defer mu.Unlock()
			require.Len(t, setnxKeys, 1)
			wantKey := "idem:" + testTenantID.String() + ":" + testUserID.String() + ":POST:" + w6cPublishPath() + ":" + rawKey
			assert.Equal(t, wantKey+":pending", setnxKeys[0])
			assert.Equal(t, 2, gets, "the stored response is read before and after the reservation")
			assert.Len(t, setKeys, 1, "the successful response is still stored for replay")
			require.Len(t, setKeys, 1)
			assert.Equal(t, wantKey, setKeys[0], "the response is stored under the key without the pending suffix")
			assert.Equal(t, tc.wantReleases, releases)

			warns := log.withPrefix("idempotency: ")
			assert.Len(t, log.withPrefix("idempotency: reservation unavailable"), tc.wantReserveWarns)
			assert.Len(t, log.withPrefix("idempotency: reading the stored response failed"), tc.wantReadWarns)
			assert.Len(t, warns, tc.wantReserveWarns+tc.wantReadWarns, "no other idempotency warning fires")
			for _, w := range warns {
				hash, ok := w.fields["cache_key_hash"].(string)
				require.True(t, ok, "warning %q carries a hashed key", w.msg)
				assert.Regexp(t, "^[0-9a-f]{16}$", hash)
				for field, v := range w.fields {
					s, _ := v.(string)
					assert.NotContains(t, s, rawKey, "field %s must not carry the client's key", field)
				}
			}
		})
	}
}

func w6cPublishCollab(deptID string) *dsl.CompiledCollaboration {
	plan := &dsl.CompiledPlan{Name: "w6c", Departments: []dsl.DepartmentDef{{ID: deptID, Label: deptID}}}
	return &dsl.CompiledCollaboration{MainPlan: plan.Name, Plans: []*dsl.CompiledPlan{plan}, SchemaVersion: dsl.CurrentSchemaVersion}
}

func TestPublishVersion_EmptyBodyOnADraftThatDivergesFromTheActiveVersion_Returns409AndPublishesNothing(t *testing.T) {
	t.Parallel()

	const draftXML = "<w6c-draft/>"
	activeID := uuid.New()
	ctrl := gomock.NewController(t)
	versions := mocks.NewMockWorkflowVersionRepository(ctrl)
	workflows := mocks.NewMockWorkflowRepository(ctrl)
	compiler := mocks.NewMockPlanCompiler(ctrl)
	svc := service.NewVersionService(service.VersionDeps{Versions: versions, Workflows: workflows, Compiler: compiler})

	draft := &domain.WorkflowVersion{
		ID: testVerID, WorkflowID: testWFID, TenantID: testTenantID,
		Status: domain.VersionStatusDraft, BPMNXML: draftXML, RecordVersion: 5,
	}
	activeNumber := int32(1)
	activePlan, err := json.Marshal(w6cPublishCollab("w6c-active-dept"))
	require.NoError(t, err)
	activePlanJSON := string(activePlan)
	active := &domain.WorkflowVersion{
		ID: activeID, WorkflowID: testWFID, TenantID: testTenantID,
		Status: domain.VersionStatusPublished, VersionNumber: &activeNumber, CompiledPlanJSON: &activePlanJSON,
	}

	versions.EXPECT().GetByID(gomock.Any(), testTenantID, testVerID).Return(draft, nil)
	compiler.EXPECT().ModuleCalls(gomock.Any(), draftXML).Return(nil, nil, nil)
	compiler.EXPECT().BundleModules(draftXML, gomock.Any()).Return(draftXML, nil)
	compiler.EXPECT().CompileForPublish(gomock.Any(), draftXML).Return(w6cPublishCollab("w6c-draft-dept"), nil)
	compiler.EXPECT().Hash(gomock.Any(), draftXML).Return("w6c-hash", nil)
	workflows.EXPECT().GetByID(gomock.Any(), testTenantID, testWFID).Return(&domain.Workflow{ID: testWFID, ActiveVersionID: &activeID}, nil)
	versions.EXPECT().GetByID(gomock.Any(), testTenantID, activeID).Return(active, nil)

	h := handler.New(handler.Services{Versions: svc})
	r := adminReq(http.MethodPost, w6cPublishPath(), nil)
	require.Zero(t, r.ContentLength)
	require.Equal(t, http.NoBody, r.Body)

	w := do(newRouter(h), r)

	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())
	prob := decodeProblem(t, w)
	assert.Equal(t, "STRUCTURAL_DIVERGENCE", prob["code"])
	assert.Equal(t, "https://api.workflow.platform/errors/conflict", prob["type"])
	assert.Equal(t, "Structural Divergence", prob["title"])
	assert.EqualValues(t, http.StatusConflict, prob["status"])
	assert.Equal(t, "structural divergence detected", prob["detail"])
	assert.Equal(t, w6cPublishPath(), prob["instance"])
	assert.NotContains(t, prob, "version_number")
}
