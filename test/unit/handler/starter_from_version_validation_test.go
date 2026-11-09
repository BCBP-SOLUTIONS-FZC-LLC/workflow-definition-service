package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

const sfvPathPrefix = "/api/v1/starters/from-workflow-version/"

type sfvRecorder struct {
	calls   int
	got     service.CreateFromWorkflowVersionReq
	tenant  uuid.UUID
	user    uuid.UUID
	version uuid.UUID
}

func (r *sfvRecorder) handler() *fakeStarterSvc {
	return &fakeStarterSvc{
		createFromWorkflowVersion: func(_ context.Context, tenantID, userID, versionID uuid.UUID, req service.CreateFromWorkflowVersionReq) (*domain.StarterTemplate, error) {
			r.calls++
			r.got = req
			r.tenant, r.user, r.version = tenantID, userID, versionID
			return &domain.StarterTemplate{
				ID: testWFID, Scope: domain.ScopeTenant,
				Name: req.Name, Description: req.Description, Category: req.Category,
			}, nil
		},
	}
}

func sfvRawAdminReq(path, body string) *http.Request {
	r := adminReq(http.MethodPost, path, nil)
	r.Body = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)).Body
	return r
}

func sfvInvalidParams(t *testing.T, prob map[string]any) []map[string]any {
	t.Helper()
	raw, ok := prob["invalid_params"].([]any)
	require.True(t, ok, "problem body carries invalid_params: %v", prob)
	out := make([]map[string]any, len(raw))
	for i, p := range raw {
		out[i], ok = p.(map[string]any)
		require.True(t, ok)
	}
	return out
}

func TestCreateStarterFromWorkflowVersion_VersionIDNotUUID_Returns400(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		versionID string
	}{
		{name: "plain text", versionID: "not-a-uuid"},
		{name: "truncated uuid", versionID: "44444444-4444-4444"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &sfvRecorder{}
			h := newStarterHandler(rec.handler())

			w := do(newRouter(h), adminReq(http.MethodPost, sfvPathPrefix+tc.versionID, map[string]any{"name": "s"}))

			require.Equal(t, http.StatusBadRequest, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, "invalid version_id: not a valid UUID", prob["detail"])
			assert.Contains(t, prob["type"], "/errors/bad-request")
			assert.Zero(t, rec.calls, "the service must not be reached")
		})
	}
}

func TestCreateStarterFromWorkflowVersion_FieldsOverLimit_Returns422(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		body        map[string]any
		wantNames   []string
		wantReasons []string
	}{
		{
			name:        "description over 2000",
			body:        map[string]any{"name": "s", "description": strings.Repeat("d", 2001)},
			wantNames:   []string{"description"},
			wantReasons: []string{"must not exceed 2000 characters"},
		},
		{
			name:        "category over 64",
			body:        map[string]any{"name": "s", "category": strings.Repeat("c", 65)},
			wantNames:   []string{"category"},
			wantReasons: []string{"must not exceed 64 characters"},
		},
		{
			name: "all three over their limits",
			body: map[string]any{
				"name":        strings.Repeat("n", 256),
				"description": strings.Repeat("d", 2001),
				"category":    strings.Repeat("c", 65),
			},
			wantNames: []string{"name", "description", "category"},
			wantReasons: []string{
				"must not exceed 255 characters",
				"must not exceed 2000 characters",
				"must not exceed 64 characters",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &sfvRecorder{}
			h := newStarterHandler(rec.handler())

			w := do(newRouter(h), adminReq(http.MethodPost, sfvPathPrefix+testVerID.String(), tc.body))

			require.Equal(t, http.StatusUnprocessableEntity, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "INVALID_INPUT", prob["code"])
			params := sfvInvalidParams(t, prob)
			require.Len(t, params, len(tc.wantNames))
			for i, p := range params {
				assert.Equal(t, tc.wantNames[i], p["name"])
				assert.Equal(t, tc.wantReasons[i], p["reason"])
			}
			assert.Zero(t, rec.calls, "the service must not be reached")
		})
	}
}

func TestCreateStarterFromWorkflowVersion_FieldsAtLimit_Returns201(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		nameVal     string
		description string
		category    string
	}{
		{
			name:        "ascii at exact limits",
			nameVal:     strings.Repeat("n", 255),
			description: strings.Repeat("d", 2000),
			category:    strings.Repeat("c", 64),
		},
		{
			name:        "multi-byte characters count as one each",
			nameVal:     strings.Repeat("é", 255),
			description: strings.Repeat("日", 2000),
			category:    strings.Repeat("ü", 64),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &sfvRecorder{}
			h := newStarterHandler(rec.handler())
			body := map[string]any{"name": tc.nameVal, "description": tc.description, "category": tc.category}

			w := do(newRouter(h), adminReq(http.MethodPost, sfvPathPrefix+testVerID.String(), body))

			require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
			require.Equal(t, 1, rec.calls)
			assert.Equal(t, testTenantID, rec.tenant)
			assert.Equal(t, testUserID, rec.user)
			assert.Equal(t, testVerID, rec.version)
			assert.Equal(t, tc.nameVal, rec.got.Name)
			assert.Equal(t, tc.description, rec.got.Description)
			assert.Equal(t, tc.category, rec.got.Category)
			assert.Equal(t, tc.nameVal, decodeProblem(t, w)["name"])
		})
	}
}

func TestCreateStarterFromWorkflowVersion_EmptyOrBlankBody_Returns400(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body string
	}{
		{name: "empty body", body: ""},
		{name: "spaces only", body: "   "},
		{name: "whitespace and newlines", body: " \n\t\r\n "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &sfvRecorder{}
			h := newStarterHandler(rec.handler())

			w := do(newRouter(h), sfvRawAdminReq(sfvPathPrefix+testVerID.String(), tc.body))

			require.Equal(t, http.StatusBadRequest, w.Code)
			prob := decodeProblem(t, w)
			assert.Equal(t, "BAD_REQUEST", prob["code"])
			assert.Equal(t, "request body is not valid JSON", prob["detail"])
			assert.Zero(t, rec.calls, "the service must not be reached")
		})
	}
}
