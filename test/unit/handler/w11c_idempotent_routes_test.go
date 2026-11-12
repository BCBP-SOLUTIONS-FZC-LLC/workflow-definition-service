package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type w11cSecrets struct {
	mu       sync.Mutex
	writes   int
	failures []error
}

func (s *w11cSecrets) Write(context.Context, string, map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	if len(s.failures) > 0 {
		err := s.failures[0]
		s.failures = s.failures[1:]
		return err
	}
	return nil
}

func (s *w11cSecrets) Delete(context.Context, string) error { return nil }

func (s *w11cSecrets) writeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

type w11cCounter struct {
	mu sync.Mutex
	n  int
}

func (c *w11cCounter) hit() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.n++
}

func (c *w11cCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func w11cEngine(svc handler.Services, cache *memCache) *gin.Engine {
	svc.Cache = cache
	svc.IdempotencyTTL = time.Hour
	return newRouter(handler.New(svc))
}

func w11cSend(r *gin.Engine, method, path string, body any, key string) *httptest.ResponseRecorder {
	rq := adminReq(method, path, body)
	rq.Header.Set("Idempotency-Key", key)
	return do(r, rq)
}

func w11cStoredResponses(c *memCache) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for k := range c.items {
		if strings.HasPrefix(k, "idem:") && !strings.HasSuffix(k, ":pending") {
			n++
		}
	}
	return n
}

func w11cWorkflowPath(suffix string) string {
	return "/api/v1/workflows/" + testWFID.String() + suffix
}

func w11cVersionPath(suffix string) string {
	return w11cWorkflowPath("/versions/" + testVerID.String() + suffix)
}

func TestIdempotentRoutes_SameKeyAndBody_ReplaysTheFirstResponseAndRunsTheServiceOnce(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		method     string
		path       string
		body       any
		wantStatus int
		wantBody   string
		build      func(calls *w11cCounter, cache *memCache) *gin.Engine
	}{
		{
			name:       "clone version",
			method:     http.MethodPost,
			path:       w11cVersionPath("/clone"),
			body:       map[string]any{"new_key": "cloned-key", "new_name": "Cloned"},
			wantStatus: http.StatusCreated,
			wantBody:   "Version cloned",
			build: func(calls *w11cCounter, cache *memCache) *gin.Engine {
				return w11cEngine(handler.Services{Versions: &fakeVersionSvc{
					clone: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, string, service.CloneReq) (*domain.Workflow, *domain.WorkflowVersion, error) {
						calls.hit()
						return newWorkflow(), newDraftVersion(), nil
					},
				}}, cache)
			},
		},
		{
			name:       "promote version",
			method:     http.MethodPost,
			path:       w11cVersionPath("/promote"),
			wantStatus: http.StatusOK,
			wantBody:   "Version promoted to active",
			build: func(calls *w11cCounter, cache *memCache) *gin.Engine {
				return w11cEngine(handler.Services{Versions: &fakeVersionSvc{
					promote: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, string) (*domain.WorkflowVersion, error) {
						calls.hit()
						return newPublishedVersion(), nil
					},
				}}, cache)
			},
		},
		{
			name:       "create starter from workflow version",
			method:     http.MethodPost,
			path:       "/api/v1/starters/from-workflow-version/" + testVerID.String(),
			body:       map[string]any{"name": "Starter from version"},
			wantStatus: http.StatusCreated,
			wantBody:   "Starter from version",
			build: func(calls *w11cCounter, cache *memCache) *gin.Engine {
				return w11cEngine(handler.Services{Starters: &fakeStarterSvc{
					createFromWorkflowVersion: func(_ context.Context, _, _, _ uuid.UUID, req service.CreateFromWorkflowVersionReq) (*domain.StarterTemplate, error) {
						calls.hit()
						return &domain.StarterTemplate{ID: uuid.New(), Name: req.Name}, nil
					},
				}}, cache)
			},
		},
		{
			name:       "archive workflow",
			method:     http.MethodPost,
			path:       w11cWorkflowPath("/archive"),
			wantStatus: http.StatusOK,
			wantBody:   "Workflow archived successfully",
			build: func(calls *w11cCounter, cache *memCache) *gin.Engine {
				return w11cEngine(handler.Services{Workflows: &fakeWorkflowSvc{
					archive: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
						calls.hit()
						return nil
					},
				}}, cache)
			},
		},
		{
			name:       "initialise draft",
			method:     http.MethodPost,
			path:       w11cWorkflowPath("/draft"),
			wantStatus: http.StatusCreated,
			wantBody:   "Draft initialized",
			build: func(calls *w11cCounter, cache *memCache) *gin.Engine {
				return w11cEngine(handler.Services{Drafts: &fakeDraftSvc{
					init: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*domain.WorkflowVersion, error) {
						calls.hit()
						return newDraftVersion(), nil
					},
				}}, cache)
			},
		},
		{
			name:       "discard draft",
			method:     http.MethodDelete,
			path:       w11cWorkflowPath("/draft"),
			wantStatus: http.StatusNoContent,
			wantBody:   "",
			build: func(calls *w11cCounter, cache *memCache) *gin.Engine {
				return w11cEngine(handler.Services{Drafts: &fakeDraftSvc{
					discard: func(context.Context, uuid.UUID, uuid.UUID) error {
						calls.hit()
						return nil
					},
				}}, cache)
			},
		},
		{
			name:       "write connector credential",
			method:     http.MethodPost,
			path:       "/api/v1/connectors/credentials",
			body:       map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "w11c-secret-value"},
			wantStatus: http.StatusCreated,
			wantBody:   "secret_path",
			build: func(calls *w11cCounter, cache *memCache) *gin.Engine {
				secrets := &w11cCountingSecrets{calls: calls}
				return w11cEngine(handler.Services{
					Connectors: service.NewConnectorService(service.ConnectorDeps{Secrets: secrets}),
				}, cache)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cache := newMemCache()
			calls := &w11cCounter{}
			r := tc.build(calls, cache)

			first := w11cSend(r, tc.method, tc.path, tc.body, "replay-key")
			require.Equal(t, tc.wantStatus, first.Code, first.Body.String())
			require.Equal(t, 1, calls.count())
			assert.Contains(t, first.Body.String(), tc.wantBody)
			assert.NotContains(t, first.Body.String(), "w11c-secret-value")
			assert.Equal(t, 1, w11cStoredResponses(cache), "the successful response is stored under the key")

			replay := w11cSend(r, tc.method, tc.path, tc.body, "replay-key")

			assert.Equal(t, tc.wantStatus, replay.Code)
			assert.Equal(t, first.Body.String(), replay.Body.String(), "the stored response is replayed as it was")
			assert.NotContains(t, replay.Body.String(), "w11c-secret-value")
			assert.Equal(t, 1, calls.count(), "the replay must not run the service again")
			assert.Equal(t, 1, w11cStoredResponses(cache))
		})
	}
}

type w11cCountingSecrets struct {
	calls *w11cCounter
}

func (s *w11cCountingSecrets) Write(context.Context, string, map[string]string) error {
	s.calls.hit()
	return nil
}

func (s *w11cCountingSecrets) Delete(context.Context, string) error { return nil }

var _ port.SecretsClient = (*w11cCountingSecrets)(nil)
var _ port.SecretsClient = (*w11cSecrets)(nil)

func TestCloneVersion_KeyReusedWithAnotherBody_Returns409AndRunsTheCloneOnce(t *testing.T) {
	t.Parallel()

	cache := newMemCache()
	calls := &w11cCounter{}
	r := w11cEngine(handler.Services{Versions: &fakeVersionSvc{
		clone: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, string, service.CloneReq) (*domain.Workflow, *domain.WorkflowVersion, error) {
			calls.hit()
			return newWorkflow(), newDraftVersion(), nil
		},
	}}, cache)
	path := w11cVersionPath("/clone")

	first := w11cSend(r, http.MethodPost, path, map[string]any{"new_key": "first-key", "new_name": "First"}, "clone-key")
	require.Equal(t, http.StatusCreated, first.Code, first.Body.String())

	other := w11cSend(r, http.MethodPost, path, map[string]any{"new_key": "second-key", "new_name": "Second"}, "clone-key")

	require.Equal(t, http.StatusConflict, other.Code, other.Body.String())
	prob := decodeProblem(t, other)
	assert.Equal(t, "IDEMPOTENCY_KEY_REPLAY", prob["code"])
	assert.Equal(t, 1, calls.count(), "a different body under a used key must not clone again")
	assert.Equal(t, 1, w11cStoredResponses(cache), "the first response stays stored")
}

func TestArchiveWorkflow_FailedAttemptsAreNotStored_TheRetryWithTheSameKeyRunsAgain(t *testing.T) {
	t.Parallel()

	cache := newMemCache()
	calls := &w11cCounter{}
	failures := []error{domain.ErrActiveInstancesExist, domain.ErrUpstreamUnavailable}
	r := w11cEngine(handler.Services{Workflows: &fakeWorkflowSvc{
		archive: func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error {
			calls.hit()
			if calls.count() <= len(failures) {
				return failures[calls.count()-1]
			}
			return nil
		},
	}}, cache)
	path := w11cWorkflowPath("/archive")

	conflict := w11cSend(r, http.MethodPost, path, nil, "archive-key")
	require.Equal(t, http.StatusConflict, conflict.Code, conflict.Body.String())
	assert.Equal(t, 0, w11cStoredResponses(cache), "a 409 is not stored")

	unavailable := w11cSend(r, http.MethodPost, path, nil, "archive-key")
	require.Equal(t, http.StatusServiceUnavailable, unavailable.Code, unavailable.Body.String())
	assert.Equal(t, 0, w11cStoredResponses(cache), "a 503 is not stored")

	retry := w11cSend(r, http.MethodPost, path, nil, "archive-key")
	require.Equal(t, http.StatusOK, retry.Code, retry.Body.String())
	assert.Equal(t, 3, calls.count(), "each failed attempt left the key free for the next one")
	assert.Equal(t, 1, w11cStoredResponses(cache), "only the success is stored")

	replay := w11cSend(r, http.MethodPost, path, nil, "archive-key")
	assert.Equal(t, http.StatusOK, replay.Code)
	assert.Equal(t, retry.Body.String(), replay.Body.String())
	assert.Equal(t, 3, calls.count(), "the success is replayed, not archived again")
}

func TestWriteConnectorCredential_SecretsStoreOutageIsNotStored_TheRetryWithTheSameKeyWritesAndReturns201(t *testing.T) {
	t.Parallel()

	cache := newMemCache()
	secrets := &w11cSecrets{failures: []error{fmt.Errorf("write secret: %w", domain.ErrUpstreamUnavailable)}}
	r := w11cEngine(handler.Services{
		Connectors: service.NewConnectorService(service.ConnectorDeps{Secrets: secrets}),
	}, cache)
	body := map[string]any{"connector_type": "send-email", "field_name": "apiKey", "value": "w11c-secret-value"}
	path := "/api/v1/connectors/credentials"

	down := w11cSend(r, http.MethodPost, path, body, "credential-key")
	require.Equal(t, http.StatusServiceUnavailable, down.Code, down.Body.String())
	assert.Equal(t, 0, w11cStoredResponses(cache), "the outage response is not stored")
	assert.NotContains(t, down.Body.String(), "w11c-secret-value")

	retry := w11cSend(r, http.MethodPost, path, body, "credential-key")

	require.Equal(t, http.StatusCreated, retry.Code, retry.Body.String())
	assert.Equal(t, 2, secrets.writeCount(), "the retry reached the secrets store again")
	assert.Equal(t, 1, w11cStoredResponses(cache))
	assert.NotContains(t, retry.Body.String(), "w11c-secret-value")
}

func TestUpdateDraft_ReplayWithTheSameKeyAndBody_DoesNotRaiseTheRecordVersionAgain(t *testing.T) {
	t.Parallel()

	cache := newMemCache()
	calls := &w11cCounter{}
	var mu sync.Mutex
	stored := int64(3)
	r := w11cEngine(handler.Services{Drafts: &fakeDraftSvc{
		update: func(_ context.Context, _, _, _ uuid.UUID, req service.UpdateDraftReq) (*domain.WorkflowVersion, error) {
			calls.hit()
			mu.Lock()
			defer mu.Unlock()
			if req.RecordVersion != stored {
				return nil, domain.ErrDraftConcurrency
			}
			stored++
			d := newDraftVersion()
			d.RecordVersion = stored
			return d, nil
		},
	}}, cache)
	body := map[string]any{"name": "Renamed", "record_version": 3}

	first := w11cSend(r, http.MethodPut, w11cWorkflowPath("/draft"), body, "update-key")
	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	assert.EqualValues(t, 4, decodeProblem(t, first)["record_version"])

	replay := w11cSend(r, http.MethodPut, w11cWorkflowPath("/draft"), body, "update-key")

	assert.Equal(t, http.StatusOK, replay.Code, replay.Body.String())
	assert.Equal(t, first.Body.String(), replay.Body.String())
	assert.EqualValues(t, 4, decodeProblem(t, replay)["record_version"])
	assert.Equal(t, 1, calls.count(), "the replay must not reach the draft service")
	mu.Lock()
	defer mu.Unlock()
	assert.EqualValues(t, 4, stored, "the record version rose once")
}

func TestDiscardDraft_IdempotencyKeyLongerThan256Bytes_Returns400AndDoesNotDiscard(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		keyLen     int
		wantStatus int
		wantCalls  int
	}{
		{name: "256 bytes is accepted", keyLen: 256, wantStatus: http.StatusNoContent, wantCalls: 1},
		{name: "257 bytes is refused", keyLen: 257, wantStatus: http.StatusBadRequest, wantCalls: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			calls := &w11cCounter{}
			r := w11cEngine(handler.Services{Drafts: &fakeDraftSvc{
				discard: func(context.Context, uuid.UUID, uuid.UUID) error {
					calls.hit()
					return nil
				},
			}}, newMemCache())

			w := w11cSend(r, http.MethodDelete, w11cWorkflowPath("/draft"), nil, strings.Repeat("k", tc.keyLen))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			assert.Equal(t, tc.wantCalls, calls.count())
			if tc.wantStatus == http.StatusBadRequest {
				prob := decodeProblem(t, w)
				assert.Equal(t, "BAD_REQUEST", prob["code"])
				assert.Equal(t, "https://api.workflow.platform/errors/bad-request", prob["type"])
				assert.Equal(t, "Idempotency-Key must be at most 256 bytes", prob["detail"])
			}
		})
	}
}
