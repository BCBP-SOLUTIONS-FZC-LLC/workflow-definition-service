package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func corruptErrorsVersion(stored string) *domain.WorkflowVersion {
	return &domain.WorkflowVersion{
		ID: uuid.New(), WorkflowID: testWFID, Status: domain.VersionStatusDraft, BPMNXML: "<bpmn/>",
		ValidationErrorsJSON: &stored,
	}
}

func corruptErrorsRoutes(version *domain.WorkflowVersion) map[string]*http.Request {
	return map[string]*http.Request{
		"GET version": req(http.MethodGet, fmt.Sprintf("/api/v1/workflows/%s/versions/%s", testWFID, version.ID), nil),
		"GET draft":   req(http.MethodGet, fmt.Sprintf("/api/v1/workflows/%s/draft", testWFID), nil),
	}
}

func newCorruptErrorsRouter(version *domain.WorkflowVersion, log *w2aFieldLogger) *gin.Engine {
	versions := &fakeVersionSvc{get: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
		return version, nil
	}}
	drafts := &fakeDraftSvc{get: func(context.Context, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
		return version, nil
	}}
	return newRouter(handler.New(handler.Services{Versions: versions, Drafts: drafts, Log: log}))
}

func TestVersionAndDraftReads_CorruptStoredValidationErrors_AnswerOKWithoutThemAndLogAWarning(t *testing.T) {
	version := corruptErrorsVersion(`{"leaky_token_xyz": "a list"`)
	log := &w2aFieldLogger{}
	router := newCorruptErrorsRouter(version, log)

	for name, request := range corruptErrorsRoutes(version) {
		t.Run(name, func(t *testing.T) {
			before := len(log.snapshot())

			w := do(router, request)

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			assert.NotContains(t, body, "validation_errors")
			assert.Equal(t, version.ID.String(), body["id"])
			warns := log.snapshot()[before:]
			require.Len(t, warns, 1)
			assert.Contains(t, warns[0].msg, "validation errors")
			assert.Equal(t, version.ID.String(), warns[0].fields["version_id"])
			assert.NotEmpty(t, warns[0].fields["error"])
			for field, v := range warns[0].fields {
				s, _ := v.(string)
				assert.NotContains(t, s, "leaky_token_xyz", "field %s must not carry the stored text", field)
			}
		})
	}
}

func TestVersionAndDraftReads_ValidStoredValidationErrors_AreShownWithoutAWarning(t *testing.T) {
	version := corruptErrorsVersion(`[{"node_id":"n1","error":"NO_START_EVENT: missing start"}]`)
	log := &w2aFieldLogger{}
	router := newCorruptErrorsRouter(version, log)

	for name, request := range corruptErrorsRoutes(version) {
		t.Run(name, func(t *testing.T) {
			w := do(router, request)

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), "NO_START_EVENT")
			assert.Empty(t, log.snapshot())
		})
	}
}

func TestVersionAndDraftReads_EmptyOrAbsentStoredValidationErrors_NeedNoWarning(t *testing.T) {
	empty := ""
	absent := corruptErrorsVersion("")
	absent.ValidationErrorsJSON = nil
	blank := corruptErrorsVersion(empty)
	for name, version := range map[string]*domain.WorkflowVersion{"absent": absent, "empty text": blank} {
		t.Run(name, func(t *testing.T) {
			log := &w2aFieldLogger{}
			router := newCorruptErrorsRouter(version, log)

			w := do(router, req(http.MethodGet, fmt.Sprintf("/api/v1/workflows/%s/draft", testWFID), nil))

			require.Equal(t, http.StatusOK, w.Code)
			assert.Empty(t, log.snapshot())
			assert.False(t, strings.Contains(w.Body.String(), "validation_errors"))
		})
	}
}

func TestVersionRead_CorruptStoredValidationErrors_WithNoLoggerConfigured_StillAnswersOK(t *testing.T) {
	version := corruptErrorsVersion(`[{"node_id"`)
	versions := &fakeVersionSvc{get: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
		return version, nil
	}}
	router := newRouter(handler.New(handler.Services{Versions: versions}))

	w := do(router, req(http.MethodGet, fmt.Sprintf("/api/v1/workflows/%s/versions/%s", testWFID, version.ID), nil))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), "validation_errors")
}
