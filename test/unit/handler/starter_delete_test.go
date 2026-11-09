package handler_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type starterDeleteCalls struct {
	gotIDs         []uuid.UUID
	deletedIDs     []uuid.UUID
	gotTenants     []uuid.UUID
	deletedTenants []uuid.UUID
}

func newStarterDeleteFake(
	calls *starterDeleteCalls,
	getErr, delErr error,
) *fakeStarterSvc {
	return &fakeStarterSvc{
		get: func(_ context.Context, tenantID, id uuid.UUID) (*domain.StarterTemplate, error) {
			calls.gotIDs = append(calls.gotIDs, id)
			calls.gotTenants = append(calls.gotTenants, tenantID)
			if getErr != nil {
				return nil, getErr
			}
			return &domain.StarterTemplate{ID: id, Scope: domain.ScopeTenant}, nil
		},
		del: func(_ context.Context, tenantID, id uuid.UUID) error {
			calls.deletedIDs = append(calls.deletedIDs, id)
			calls.deletedTenants = append(calls.deletedTenants, tenantID)
			return delErr
		},
	}
}

func TestDeleteStarter_LookupOrDeleteFails_ReturnsTheMappedProblem(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		path        string
		getErr      error
		delErr      error
		wantStatus  int
		wantCode    string
		wantDetail  string
		wantGetCall bool
		wantDelCall bool
	}{
		{
			name:       "invalid uuid is refused before any lookup",
			path:       "not-a-uuid",
			wantStatus: http.StatusBadRequest,
			wantCode:   "BAD_REQUEST",
			wantDetail: "invalid id: not a valid UUID",
		},
		{
			name:        "nil uuid is not found",
			path:        uuid.Nil.String(),
			getErr:      domain.ErrNotFound,
			wantStatus:  http.StatusNotFound,
			wantCode:    "NOT_FOUND",
			wantGetCall: true,
		},
		{
			name:        "starter removed between lookup and delete is not found",
			path:        testWFID.String(),
			delErr:      domain.ErrNotFound,
			wantStatus:  http.StatusNotFound,
			wantCode:    "NOT_FOUND",
			wantGetCall: true,
			wantDelCall: true,
		},
		{
			name:        "database unreachable at lookup is unavailable",
			path:        testWFID.String(),
			getErr:      domain.ErrUpstreamUnavailable,
			wantStatus:  http.StatusServiceUnavailable,
			wantCode:    "UPSTREAM_UNAVAILABLE",
			wantDetail:  "an upstream dependency is temporarily unavailable",
			wantGetCall: true,
		},
		{
			name:        "database unreachable at delete is unavailable",
			path:        testWFID.String(),
			delErr:      domain.ErrUpstreamUnavailable,
			wantStatus:  http.StatusServiceUnavailable,
			wantCode:    "UPSTREAM_UNAVAILABLE",
			wantDetail:  "an upstream dependency is temporarily unavailable",
			wantGetCall: true,
			wantDelCall: true,
		},
		{
			name:        "unclassified lookup error is internal and not echoed",
			path:        testWFID.String(),
			getErr:      errors.New("secret driver detail"),
			wantStatus:  http.StatusInternalServerError,
			wantCode:    "INTERNAL_ERROR",
			wantDetail:  "an unexpected error occurred",
			wantGetCall: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := &starterDeleteCalls{}
			h := newStarterHandler(newStarterDeleteFake(calls, tc.getErr, tc.delErr))

			w := do(newRouter(h), adminReq(http.MethodDelete, "/api/v1/starters/"+tc.path, nil))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, tc.wantCode, prob["code"])
			assert.EqualValues(t, tc.wantStatus, prob["status"])
			if tc.wantDetail != "" {
				assert.Equal(t, tc.wantDetail, prob["detail"])
			}
			assert.Equal(t, tc.wantGetCall, len(calls.gotIDs) > 0, "lookup called")
			assert.Equal(t, tc.wantDelCall, len(calls.deletedIDs) > 0, "delete called")
		})
	}
}

func TestDeleteStarter_UuidSpelling_DeletesTheParsedIDAndEchoesItNormalised(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
	cases := []struct {
		name string
		path string
	}{
		{name: "uppercase", path: "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE"},
		{name: "undashed", path: "aaaaaaaabbbbccccddddeeeeeeeeeeee"},
		{name: "undashed uppercase", path: "AAAAAAAABBBBCCCCDDDDEEEEEEEEEEEE"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			calls := &starterDeleteCalls{}
			h := newStarterHandler(newStarterDeleteFake(calls, nil, nil))

			w := do(newRouter(h), adminReq(http.MethodDelete, "/api/v1/starters/"+tc.path, nil))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, id.String(), prob["starter_id"])
			assert.Equal(t, "Starter deleted", prob["message"])
			assert.Equal(t, []uuid.UUID{id}, calls.gotIDs)
			assert.Equal(t, []uuid.UUID{id}, calls.deletedIDs)
			assert.Equal(t, []uuid.UUID{testTenantID}, calls.gotTenants)
			assert.Equal(t, []uuid.UUID{testTenantID}, calls.deletedTenants)
		})
	}
}

func TestDeleteStarter_SecondDelete_ReturnsNotFound(t *testing.T) {
	t.Parallel()

	present := true
	h := newStarterHandler(&fakeStarterSvc{
		get: func(_ context.Context, _, id uuid.UUID) (*domain.StarterTemplate, error) {
			if !present {
				return nil, domain.ErrNotFound
			}
			return &domain.StarterTemplate{ID: id, Scope: domain.ScopeTenant}, nil
		},
		del: func(context.Context, uuid.UUID, uuid.UUID) error {
			present = false
			return nil
		},
	})
	router := newRouter(h)
	path := "/api/v1/starters/" + testWFID.String()

	first := do(router, adminReq(http.MethodDelete, path, nil))
	second := do(router, adminReq(http.MethodDelete, path, nil))

	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	assert.Equal(t, testWFID.String(), decodeProblem(t, first)["starter_id"])
	require.Equal(t, http.StatusNotFound, second.Code, second.Body.String())
	assert.Equal(t, "NOT_FOUND", decodeProblem(t, second)["code"])
}
