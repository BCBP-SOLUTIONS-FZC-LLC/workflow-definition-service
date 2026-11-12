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
	"time"

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

type w6dParam struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type w6dProblem struct {
	Code          string     `json:"code"`
	Detail        string     `json:"detail"`
	Status        int        `json:"status"`
	Instance      string     `json:"instance"`
	InvalidParams []w6dParam `json:"invalid_params"`
}

func w6dDecodeProblem(t *testing.T, w *httptest.ResponseRecorder) w6dProblem {
	t.Helper()
	var p w6dProblem
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p), w.Body.String())
	return p
}

func w6dVersionPath(action string) string {
	return "/api/v1/workflows/" + testWFID.String() + "/versions/" + testVerID.String() + "/" + action
}

func w6dRawAdminReq(method, path, raw string) *http.Request {
	r := adminReq(method, path, nil)
	r.Body = http.NoBody
	if raw != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("x-tenant-id", testTenantID.String())
		r.Header.Set("x-user-id", testUserID.String())
		r.Header.Set("x-tenant-roles", "tenant_admin")
	}
	return r
}

type w6dPublishSpy struct {
	calls int
}

func (s *w6dPublishSpy) handler() *handler.Handler {
	return newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		publish: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, bool) (*domain.WorkflowVersion, error) {
			s.calls++
			return newPublishedVersion(), nil
		},
	}, &fakeValidationSvc{})
}

func TestPublishVersion_DataAfterTheJSONValue_IsRefusedAsInvalidJSON(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{name: "a second object", body: `{"force_publish_structural":true}{"force_publish_structural":false}`},
		{name: "an empty object followed by an empty object", body: `{}{}`},
		{name: "a value after whitespace", body: "{}\n  7"},
		{name: "plain text after the object", body: `{} trailing`},
		{name: "null after the object", body: `{}null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spy := &w6dPublishSpy{}

			w := do(newRouter(spy.handler()), w6dRawAdminReq(http.MethodPost, w6dVersionPath("publish"), tc.body))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Equal(t, "application/problem+json", strings.Split(w.Header().Get("Content-Type"), ";")[0])
			p := w6dDecodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", p.Code)
			assert.Equal(t, "request body is not valid JSON", p.Detail)
			assert.Empty(t, p.InvalidParams, "trailing data names no field")
			assert.Zero(t, spy.calls, "a body with a second value must not reach the service")
		})
	}
}

func TestPublishVersion_BodyThatIsNotAnObject_NamesTheParameterBody(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{name: "an empty array", body: `[]`},
		{name: "an array holding the flag object", body: `[{"force_publish_structural":true}]`},
		{name: "a string", body: `"force_publish_structural"`},
		{name: "a number", body: `5`},
		{name: "a boolean", body: `true`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spy := &w6dPublishSpy{}

			w := do(newRouter(spy.handler()), w6dRawAdminReq(http.MethodPost, w6dVersionPath("publish"), tc.body))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			p := w6dDecodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", p.Code)
			assert.Equal(t, "request body has a field of the wrong type", p.Detail)
			require.Len(t, p.InvalidParams, 1)
			assert.Equal(t, w6dParam{Name: "body", Reason: "has the wrong type"}, p.InvalidParams[0])
			assert.Zero(t, spy.calls, "a body that is not an object must not reach the service")
		})
	}
}

func TestPublishVersion_UnknownFields_AreReportedSortedAndCappedAtTwenty(t *testing.T) {
	t.Parallel()

	descending := func(count int) string {
		var b strings.Builder
		b.WriteString(`{"force_publish_structural":true`)
		for i := count; i >= 1; i-- {
			fmt.Fprintf(&b, `,"unknown_%02d":1`, i)
		}
		b.WriteString("}")
		return b.String()
	}
	firstTwenty := make([]string, 0, 20)
	for i := 1; i <= 20; i++ {
		firstTwenty = append(firstTwenty, fmt.Sprintf("unknown_%02d", i))
	}

	cases := []struct {
		name      string
		body      string
		wantNames []string
	}{
		{name: "twenty-five unknown fields keep the first twenty by name", body: descending(25), wantNames: firstTwenty},
		{name: "exactly twenty are all reported", body: descending(20), wantNames: firstTwenty},
		{name: "twenty-one report the first twenty", body: descending(21), wantNames: firstTwenty},
		{name: "three are reported in name order", body: descending(3), wantNames: []string{"unknown_01", "unknown_02", "unknown_03"}},
		{
			name:      "a name of a hundred characters is cut to sixty-four",
			body:      `{"` + strings.Repeat("x", 100) + `":1}`,
			wantNames: []string{strings.Repeat("x", 64)},
		},
		{
			name:      "a name of a hundred multi-byte characters is cut to sixty-four characters",
			body:      `{"` + strings.Repeat("é", 100) + `":1}`,
			wantNames: []string{strings.Repeat("é", 64)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spy := &w6dPublishSpy{}

			w := do(newRouter(spy.handler()), w6dRawAdminReq(http.MethodPost, w6dVersionPath("publish"), tc.body))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			p := w6dDecodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", p.Code)
			assert.Equal(t, "request body has fields this endpoint does not define", p.Detail)
			got := make([]string, 0, len(p.InvalidParams))
			for _, param := range p.InvalidParams {
				assert.Equal(t, "is not a known field", param.Reason)
				got = append(got, param.Name)
			}
			assert.Equal(t, tc.wantNames, got)
			assert.Zero(t, spy.calls, "unknown fields must not reach the service")
		})
	}
}

func TestPublishVersion_FlagKeyInAnotherCase_IsRefusedAsUnknown(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"FORCE_PUBLISH_STRUCTURAL", "Force_Publish_Structural", "forcePublishStructural"} {
		t.Run(key, func(t *testing.T) {
			t.Parallel()
			spy := &w6dPublishSpy{}

			w := do(newRouter(spy.handler()), w6dRawAdminReq(http.MethodPost, w6dVersionPath("publish"), `{"`+key+`":true}`))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			p := w6dDecodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", p.Code)
			assert.Equal(t, "request body has fields this endpoint does not define", p.Detail)
			require.Len(t, p.InvalidParams, 1)
			assert.Equal(t, w6dParam{Name: key, Reason: "is not a known field"}, p.InvalidParams[0])
			assert.Zero(t, spy.calls, "a flag spelled in another case must not publish")
		})
	}
}

func TestPublishVersion_FlagOfTheWrongType_NamesTheFlag(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body string
	}{
		{name: "a string", body: `{"force_publish_structural":"true"}`},
		{name: "a number", body: `{"force_publish_structural":1}`},
		{name: "an object", body: `{"force_publish_structural":{}}`},
		{name: "an array", body: `{"force_publish_structural":[true]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			spy := &w6dPublishSpy{}

			w := do(newRouter(spy.handler()), w6dRawAdminReq(http.MethodPost, w6dVersionPath("publish"), tc.body))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			p := w6dDecodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", p.Code)
			assert.Equal(t, "request body has a field of the wrong type", p.Detail)
			require.Len(t, p.InvalidParams, 1)
			assert.Equal(t, w6dParam{Name: "force_publish_structural", Reason: "has the wrong type"}, p.InvalidParams[0])
			assert.Zero(t, spy.calls, "a mistyped flag must not reach the service")
		})
	}
}

type w6dPublishCall struct {
	tenant, user, workflow, version uuid.UUID
}

func TestPublishVersion_SameIdempotencyKeyFromAnotherCaller_IsNotReplayed(t *testing.T) {
	t.Parallel()

	otherTenant := uuid.MustParse("66666666-6666-6666-6666-666666666666")
	otherUser := uuid.MustParse("77777777-7777-7777-7777-777777777777")
	cases := []struct {
		name   string
		tenant uuid.UUID
		user   uuid.UUID
	}{
		{name: "another user in the same tenant", tenant: testTenantID, user: otherUser},
		{name: "the same user id in another tenant", tenant: otherTenant, user: testUserID},
		{name: "another user in another tenant", tenant: otherTenant, user: otherUser},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var calls []w6dPublishCall
			h := handler.New(handler.Services{
				Versions: &fakeVersionSvc{
					publish: func(_ context.Context, tenantID, userID, workflowID, versionID uuid.UUID, _ bool) (*domain.WorkflowVersion, error) {
						calls = append(calls, w6dPublishCall{tenantID, userID, workflowID, versionID})
						if len(calls) == 1 {
							return newPublishedVersion(), nil
						}
						return nil, domain.ErrVersionAlreadyPublished
					},
				},
				Cache:          newMemCache(),
				IdempotencyTTL: time.Hour,
			})
			router := newRouter(h)
			send := func(tenant, user uuid.UUID) *httptest.ResponseRecorder {
				r := adminReq(http.MethodPost, w6dVersionPath("publish"), nil)
				r.Header.Set("x-tenant-id", tenant.String())
				r.Header.Set("x-user-id", user.String())
				r.Header.Set("Idempotency-Key", "shared-key")
				return do(router, r)
			}

			first := send(testTenantID, testUserID)
			require.Equal(t, http.StatusOK, first.Code, first.Body.String())
			require.Len(t, calls, 1)

			replay := send(testTenantID, testUserID)
			require.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
			assert.Equal(t, first.Body.String(), replay.Body.String(), "the same caller gets the stored response")
			require.Len(t, calls, 1, "the same caller's retry is served from the stored response")

			other := send(tc.tenant, tc.user)

			require.Equal(t, http.StatusConflict, other.Code, other.Body.String())
			p := w6dDecodeProblem(t, other)
			assert.Equal(t, "INVALID_VERSION_STATUS", p.Code)
			assert.Equal(t, domain.ErrVersionAlreadyPublished.Error(), p.Detail)
			require.Len(t, calls, 2, "another caller's request runs its own publish")
			assert.Equal(t, w6dPublishCall{tenant: tc.tenant, user: tc.user, workflow: testWFID, version: testVerID}, calls[1])
		})
	}
}

type w6dPlanLimits map[string]*int64

func (l w6dPlanLimits) WorkflowTemplateLimit(_ context.Context, planCode string) (*int64, error) {
	limit, ok := l[planCode]
	if !ok {
		return nil, domain.ErrUpstreamUnavailable
	}
	return limit, nil
}

type w6dCloneFixture struct {
	router   *gin.Engine
	storedWF *domain.Workflow
	storedV  *domain.WorkflowVersion
}

func w6dNewCloneFixture(t *testing.T, source *domain.WorkflowVersion) *w6dCloneFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	fx := &w6dCloneFixture{}
	tx := mocks.NewMockTransactor(ctrl)
	tx.EXPECT().RunInTxWithRetry(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	wfRepo := mocks.NewMockWorkflowRepository(ctrl)
	vRepo := mocks.NewMockWorkflowVersionRepository(ctrl)
	vRepo.EXPECT().GetByID(gomock.Any(), testTenantID, source.ID).Return(source, nil)
	wfRepo.EXPECT().CountByTenant(gomock.Any(), testTenantID).Return(int64(4), nil)
	wfRepo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, w *domain.Workflow) error {
			fx.storedWF = w
			return nil
		})
	vRepo.EXPECT().Create(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, v *domain.WorkflowVersion) error {
			fx.storedV = v
			return nil
		})
	limit := int64(5)
	svc := service.NewVersionService(service.VersionDeps{
		PlanLimits: w6dPlanLimits{"starter": &limit},
		Transactor: tx,
		Workflows:  wfRepo,
		Versions:   vRepo,
	})
	fx.router = newRouter(handler.New(handler.Services{Versions: svc}))
	return fx
}

func w6dPublishedSource() *domain.WorkflowVersion {
	v := newPublishedVersion()
	v.BPMNXML = `<definitions id="source-diagram"/>`
	return v
}

func w6dCloneReq(body any) *http.Request {
	r := adminReq(http.MethodPost, w6dVersionPath("clone"), body)
	r.Header.Set("x-plan", "starter")
	return r
}

func TestCloneVersion_PublishedSourceUnderTheQuota_StoresAFreshDraftCopy(t *testing.T) {
	t.Parallel()
	source := w6dPublishedSource()
	fx := w6dNewCloneFixture(t, source)

	w := do(fx.router, w6dCloneReq(map[string]any{
		"new_key": "copy-key", "new_name": "Copy Name", "new_description": "Copy description",
	}))

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	require.NotNil(t, fx.storedWF)
	require.NotNil(t, fx.storedV)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, "Version cloned", body["message"])
	assert.Equal(t, string(domain.VersionStatusDraft), body["status"])
	versionNumber, present := body["version_number"]
	assert.True(t, present, "version_number is part of the response")
	assert.Nil(t, versionNumber, "a draft copy has no version number")

	assert.NotEqual(t, uuid.Nil, fx.storedWF.ID)
	assert.NotEqual(t, testWFID, fx.storedWF.ID, "the copy is a new workflow")
	assert.NotEqual(t, testVerID, fx.storedV.ID, "the copy is a new version")
	assert.Equal(t, fx.storedWF.ID.String(), body["workflow_id"])
	assert.Equal(t, fx.storedV.ID.String(), body["version_id"])

	assert.Equal(t, "copy-key", fx.storedWF.TemplateKey)
	assert.Equal(t, "Copy Name", fx.storedWF.Name)
	assert.Equal(t, "Copy description", fx.storedWF.Description)
	assert.Equal(t, testTenantID, fx.storedWF.TenantID)
	assert.Equal(t, testUserID, fx.storedWF.CreatedByUserID)

	assert.Equal(t, fx.storedWF.ID, fx.storedV.WorkflowID)
	assert.Equal(t, domain.VersionStatusDraft, fx.storedV.Status)
	assert.Nil(t, fx.storedV.VersionNumber)
	assert.True(t, fx.storedV.IsValid, "a clone starts out valid")
	assert.Equal(t, source.BPMNXML, fx.storedV.BPMNXML)
	assert.Equal(t, testTenantID, fx.storedV.TenantID)
	assert.Equal(t, testUserID, fx.storedV.CreatedByUserID)
}

func TestCloneVersion_DescriptionOmittedEmptyOrGiven_StoresWhatWasGivenOrEmpty(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		body     map[string]any
		wantDesc string
	}{
		{name: "omitted", body: map[string]any{"new_key": "k", "new_name": "n"}, wantDesc: ""},
		{name: "empty string", body: map[string]any{"new_key": "k", "new_name": "n", "new_description": ""}, wantDesc: ""},
		{name: "given", body: map[string]any{"new_key": "k", "new_name": "n", "new_description": "kept"}, wantDesc: "kept"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fx := w6dNewCloneFixture(t, w6dPublishedSource())

			w := do(fx.router, w6dCloneReq(tc.body))

			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			require.NotNil(t, fx.storedWF)
			assert.Equal(t, tc.wantDesc, fx.storedWF.Description)
		})
	}
}

func TestCloneVersion_MissingNewKeyOrNewName_NamesEachMissingField(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		body map[string]any
		want []w6dParam
	}{
		{name: "new_key omitted", body: map[string]any{"new_name": "n"}, want: []w6dParam{{"new_key", "is required"}}},
		{name: "new_key empty", body: map[string]any{"new_key": "", "new_name": "n"}, want: []w6dParam{{"new_key", "is required"}}},
		{name: "new_name omitted", body: map[string]any{"new_key": "k"}, want: []w6dParam{{"new_name", "is required"}}},
		{name: "new_name empty", body: map[string]any{"new_key": "k", "new_name": ""}, want: []w6dParam{{"new_name", "is required"}}},
		{
			name: "both omitted, only the description given",
			body: map[string]any{"new_description": "d"},
			want: []w6dParam{{"new_key", "is required"}, {"new_name", "is required"}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
				clone: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, string, service.CloneReq) (*domain.Workflow, *domain.WorkflowVersion, error) {
					calls++
					return newWorkflow(), newDraftVersion(), nil
				},
			}, &fakeValidationSvc{})

			w := do(newRouter(h), w6dCloneReq(tc.body))

			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			p := w6dDecodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", p.Code)
			assert.Equal(t, "request body has missing or invalid fields", p.Detail)
			assert.Equal(t, tc.want, p.InvalidParams)
			assert.Zero(t, calls, "a clone request without its key or name must not reach the service")
		})
	}
}

func TestPromoteVersion_UnclassifiedServiceError_Is500WithoutTheInternalText(t *testing.T) {
	t.Parallel()
	logger := &fakeLogger{}
	var gotTenant, gotUser, gotWorkflow, gotVersion uuid.UUID
	h := handler.New(handler.Services{
		Versions: &fakeVersionSvc{
			promote: func(_ context.Context, tenantID, userID, workflowID, versionID uuid.UUID, _ string) (*domain.WorkflowVersion, error) {
				gotTenant, gotUser, gotWorkflow, gotVersion = tenantID, userID, workflowID, versionID
				return nil, errors.New("tx aborted: deadlock detected on relation workflow_version")
			},
		},
		Log: logger,
	})

	w := do(newRouter(h), adminReq(http.MethodPost, w6dVersionPath("promote"), nil))

	require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
	assert.Equal(t, "application/problem+json", strings.Split(w.Header().Get("Content-Type"), ";")[0])
	p := w6dDecodeProblem(t, w)
	assert.Equal(t, "INTERNAL_ERROR", p.Code)
	assert.Equal(t, "an unexpected error occurred", p.Detail)
	assert.Equal(t, w6dVersionPath("promote"), p.Instance)
	assert.Equal(t, http.StatusInternalServerError, p.Status)
	assert.NotContains(t, w.Body.String(), "deadlock")
	assert.Equal(t, []string{"request failed"}, logger.errorCalls, "the cause is logged, not returned")
	assert.Equal(t, [4]uuid.UUID{testTenantID, testUserID, testWFID, testVerID}, [4]uuid.UUID{gotTenant, gotUser, gotWorkflow, gotVersion})
}

func TestPromoteVersion_Success_EchoesThePathIdsAndThePromotedNumber(t *testing.T) {
	t.Parallel()
	number := int32(7)
	promoted := newPublishedVersion()
	promoted.ID = uuid.New()
	promoted.WorkflowID = uuid.New()
	promoted.VersionNumber = &number
	var gotTenant, gotUser, gotWorkflow, gotVersion uuid.UUID
	h := newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, &fakeVersionSvc{
		promote: func(_ context.Context, tenantID, userID, workflowID, versionID uuid.UUID, _ string) (*domain.WorkflowVersion, error) {
			gotTenant, gotUser, gotWorkflow, gotVersion = tenantID, userID, workflowID, versionID
			return promoted, nil
		},
	}, &fakeValidationSvc{})

	w := do(newRouter(h), adminReq(http.MethodPost, w6dVersionPath("promote"), nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, map[string]any{
		"workflow_id":       testWFID.String(),
		"active_version_id": testVerID.String(),
		"version_number":    float64(7),
		"message":           "Version promoted to active",
	}, body)
	assert.Equal(t, [4]uuid.UUID{testTenantID, testUserID, testWFID, testVerID}, [4]uuid.UUID{gotTenant, gotUser, gotWorkflow, gotVersion})
}

func w6dValidationRouter(t *testing.T, version *domain.WorkflowVersion, getErr error, isValid bool, issues []domain.BPMNValidationError, validateErr error, log *fakeLogger) (*gin.Engine, *[]uuid.UUID) {
	t.Helper()
	var seen []uuid.UUID
	services := handler.Services{
		Versions: &fakeVersionSvc{
			get: func(_ context.Context, tenantID, workflowID, versionID uuid.UUID) (*domain.WorkflowVersion, error) {
				seen = []uuid.UUID{tenantID, workflowID, versionID}
				return version, getErr
			},
		},
		Validation: &fakeValidationSvc{
			validateVersion: func(context.Context, uuid.UUID, *domain.WorkflowVersion) (bool, []domain.BPMNValidationError, error) {
				return isValid, issues, validateErr
			},
		},
	}
	if log != nil {
		services.Log = log
	}
	return newRouter(handler.New(services)), &seen
}

func TestGetVersionValidation_IssuesAreMappedFieldByField(t *testing.T) {
	t.Parallel()
	issues := []domain.BPMNValidationError{
		{Code: domain.BPMNErrNoStartEvent, NodeID: "Process_1", Message: "process has no start event", Severity: domain.SeverityError},
		{Code: domain.BPMNWarnUnknownStageType, NodeID: "Task_audit", Message: "stage type is not known", Severity: domain.SeverityWarning},
		{Code: domain.BPMNErrCycleDetected, NodeID: "", Message: "the diagram has a cycle", Severity: domain.SeverityError},
	}
	router, seen := w6dValidationRouter(t, newPublishedVersion(), nil, false, issues, nil, nil)

	w := do(router, req(http.MethodGet, w6dVersionPath("validation"), nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body struct {
		VersionID string           `json:"version_id"`
		Status    string           `json:"status"`
		IsValid   bool             `json:"is_valid"`
		Issues    []map[string]any `json:"issues"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, testVerID.String(), body.VersionID)
	assert.Equal(t, "PUBLISHED", body.Status)
	assert.False(t, body.IsValid)
	assert.Equal(t, []map[string]any{
		{"node_id": "Process_1", "code": string(domain.BPMNErrNoStartEvent), "message": "process has no start event", "severity": string(domain.SeverityError)},
		{"node_id": "Task_audit", "code": string(domain.BPMNWarnUnknownStageType), "message": "stage type is not known", "severity": string(domain.SeverityWarning)},
		{"node_id": "", "code": string(domain.BPMNErrCycleDetected), "message": "the diagram has a cycle", "severity": string(domain.SeverityError)},
	}, body.Issues, "each issue carries exactly node_id, code, message and severity, in order")
	assert.Equal(t, []uuid.UUID{testTenantID, testWFID, testVerID}, *seen)
}

func TestGetVersionValidation_NoIssues_AnswersAnEmptyArrayNotNull(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		issues []domain.BPMNValidationError
	}{
		{name: "a nil issue list", issues: nil},
		{name: "an empty issue list", issues: []domain.BPMNValidationError{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			router, _ := w6dValidationRouter(t, newDraftVersion(), nil, true, tc.issues, nil, nil)

			w := do(router, req(http.MethodGet, w6dVersionPath("validation"), nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var raw map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
			assert.JSONEq(t, `[]`, string(raw["issues"]))
		})
	}
}

func TestGetVersionValidation_DraftWithNoIssues_IsValid(t *testing.T) {
	t.Parallel()
	router, seen := w6dValidationRouter(t, newDraftVersion(), nil, true, nil, nil, nil)

	w := do(router, req(http.MethodGet, w6dVersionPath("validation"), nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))
	assert.JSONEq(t, `true`, string(raw["is_valid"]))
	assert.JSONEq(t, `[]`, string(raw["issues"]))
	assert.JSONEq(t, `"DRAFT"`, string(raw["status"]))
	assert.JSONEq(t, `"`+testVerID.String()+`"`, string(raw["version_id"]))
	assert.Equal(t, []uuid.UUID{testTenantID, testWFID, testVerID}, *seen)
}

func TestGetVersionValidation_WarningOnly_IsStillValidAndListsTheWarning(t *testing.T) {
	t.Parallel()
	issues := []domain.BPMNValidationError{
		{Code: domain.BPMNWarnUnknownStageType, NodeID: "Task_audit", Message: "stage type is not known", Severity: domain.SeverityWarning},
	}
	router, _ := w6dValidationRouter(t, newDraftVersion(), nil, true, issues, nil, nil)

	w := do(router, req(http.MethodGet, w6dVersionPath("validation"), nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body struct {
		IsValid bool             `json:"is_valid"`
		Issues  []map[string]any `json:"issues"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.True(t, body.IsValid, "the handler reports the service's verdict, not the issue count")
	assert.Equal(t, []map[string]any{
		{"node_id": "Task_audit", "code": string(domain.BPMNWarnUnknownStageType), "message": "stage type is not known", "severity": string(domain.SeverityWarning)},
	}, body.Issues)
}

func TestGetVersionValidation_UnexpectedFailure_Is500WithoutTheInternalText(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		getErr      error
		validateErr error
	}{
		{name: "loading the version fails", getErr: errors.New("scan workflow_version: unexpected column type")},
		{name: "validating the version fails", validateErr: errors.New("compiler panic: index out of range")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			logger := &fakeLogger{}
			router, seen := w6dValidationRouter(t, newDraftVersion(), tc.getErr, false, nil, tc.validateErr, logger)

			w := do(router, req(http.MethodGet, w6dVersionPath("validation"), nil))

			require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
			p := w6dDecodeProblem(t, w)
			assert.Equal(t, "INTERNAL_ERROR", p.Code)
			assert.Equal(t, "an unexpected error occurred", p.Detail)
			assert.Equal(t, w6dVersionPath("validation"), p.Instance)
			assert.NotContains(t, w.Body.String(), "unexpected column")
			assert.NotContains(t, w.Body.String(), "panic")
			assert.Equal(t, []string{"request failed"}, logger.errorCalls)
			assert.Equal(t, []uuid.UUID{testTenantID, testWFID, testVerID}, *seen)
		})
	}
}
