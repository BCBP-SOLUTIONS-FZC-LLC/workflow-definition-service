package handler_test

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type sgbLogEntry struct {
	msg    string
	fields map[string]any
}

type sgbRecordingLogger struct {
	mu     sync.Mutex
	errors []sgbLogEntry
}

func (l *sgbRecordingLogger) Debug(string, map[string]any) { /* no-op */ }
func (l *sgbRecordingLogger) Info(string, map[string]any)  { /* no-op */ }
func (l *sgbRecordingLogger) Warn(string, map[string]any)  { /* no-op */ }
func (l *sgbRecordingLogger) Fatal(string, map[string]any) { /* no-op */ }
func (l *sgbRecordingLogger) Error(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, sgbLogEntry{msg: msg, fields: fields})
}

func (l *sgbRecordingLogger) recorded() []sgbLogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]sgbLogEntry(nil), l.errors...)
}

func TestDiscardDraft_RealService_RepositoryFailure_ReturnsMappedProblem(t *testing.T) {
	t.Parallel()

	const rawDBText = "pq: relation workflow_version is corrupt at /var/lib/postgres"
	cases := []struct {
		name         string
		lookupErr    error
		deleteErr    error
		wantStatus   int
		wantCode     string
		wantDetail   string
		wantNotInBdy string
	}{
		{
			name:         "unclassified error on the draft lookup",
			lookupErr:    errors.New(rawDBText),
			wantStatus:   http.StatusInternalServerError,
			wantCode:     "INTERNAL_ERROR",
			wantDetail:   "an unexpected error occurred",
			wantNotInBdy: "corrupt",
		},
		{
			name:         "unclassified error on the delete",
			deleteErr:    errors.New(rawDBText),
			wantStatus:   http.StatusInternalServerError,
			wantCode:     "INTERNAL_ERROR",
			wantDetail:   "an unexpected error occurred",
			wantNotInBdy: "corrupt",
		},
		{
			name:         "database unavailable on the draft lookup",
			lookupErr:    fmt.Errorf("%w: dial tcp 10.0.0.7:5432: connection refused", domain.ErrUpstreamUnavailable),
			wantStatus:   http.StatusServiceUnavailable,
			wantCode:     "UPSTREAM_UNAVAILABLE",
			wantDetail:   "an upstream dependency is temporarily unavailable",
			wantNotInBdy: "10.0.0.7",
		},
		{
			name:         "database unavailable on the delete",
			deleteErr:    fmt.Errorf("%w: dial tcp 10.0.0.7:5432: connection refused", domain.ErrUpstreamUnavailable),
			wantStatus:   http.StatusServiceUnavailable,
			wantCode:     "UPSTREAM_UNAVAILABLE",
			wantDetail:   "an upstream dependency is temporarily unavailable",
			wantNotInBdy: "10.0.0.7",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			versions := mocks.NewMockWorkflowVersionRepository(ctrl)
			draft := newDraftVersion()
			if tc.lookupErr != nil {
				versions.EXPECT().GetDraft(gomock.Any(), testTenantID, testWFID).Return(nil, tc.lookupErr)
			} else {
				versions.EXPECT().GetDraft(gomock.Any(), testTenantID, testWFID).Return(draft, nil)
				versions.EXPECT().DeleteDraft(gomock.Any(), testTenantID, draft.ID).Return(tc.deleteErr)
			}
			log := &sgbRecordingLogger{}
			svc := service.NewDraftService(service.DraftDeps{Versions: versions})
			router := newRouter(handler.New(handler.Services{Drafts: svc, Log: log}))
			path := "/api/v1/workflows/" + testWFID.String() + "/draft"

			w := do(router, adminReq(http.MethodDelete, path, nil))

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			prob := decodeProblem(t, w)
			assert.Equal(t, tc.wantCode, prob["code"])
			assert.Equal(t, tc.wantDetail, prob["detail"])
			assert.Equal(t, float64(tc.wantStatus), prob["status"])
			assert.NotContains(t, w.Body.String(), tc.wantNotInBdy)

			entries := log.recorded()
			require.Len(t, entries, 1, "a 5xx answer logs the failure once")
			assert.Equal(t, tc.wantStatus, entries[0].fields["status"])
			assert.Equal(t, tc.wantCode, entries[0].fields["code"])
			assert.Equal(t, testTenantID.String(), entries[0].fields["tenant_id"])
			assert.Equal(t, testUserID.String(), entries[0].fields["user_id"])
			assert.Equal(t, path, entries[0].fields["path"])
			assert.Contains(t, fmt.Sprint(entries[0].fields["error"]), tc.wantNotInBdy, "the log keeps the real cause that the body hides")
		})
	}
}
