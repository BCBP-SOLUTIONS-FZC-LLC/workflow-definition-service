//go:build integration

package postgres_test

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

type w13aPointerCallCounter struct {
	port.WorkflowRepository
	calls atomic.Int32
}

func (c *w13aPointerCallCounter) UpdateActiveVersion(ctx context.Context, tenantID, workflowID uuid.UUID, versionID *uuid.UUID) error {
	c.calls.Add(1)
	return c.WorkflowRepository.UpdateActiveVersion(ctx, tenantID, workflowID, versionID)
}

func TestArchiveWorkflow_HTTP_ARepositoryWriteInsideTheTransactionFails_IsMappedAndTheVersionStaysPublished(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)

	cases := []struct {
		name         string
		txPool       func(t *testing.T, tenantID, userID uuid.UUID) (*pgcommon.Pool, *sgbCuttingProxy)
		wantStatus   int
		wantType     string
		wantCode     string
		wantDetail   string
		wantCut      bool
		wantPointers int32
	}{
		{
			name: "the version archive is refused by the database",
			txPool: func(t *testing.T, tenantID, userID uuid.UUID) (*pgcommon.Pool, *sgbCuttingProxy) {
				return w10bRestrictedPool(t, superPool, dsn, tenantID, userID, "REVOKE UPDATE ON workflow_version FROM {role}"), nil
			},
			wantStatus: http.StatusInternalServerError,
			wantType:   w10bProblemInternal,
			wantCode:   "INTERNAL_ERROR",
			wantDetail: "an unexpected error occurred",
		},
		{
			name: "the connection is lost during the version archive",
			txPool: func(t *testing.T, _, _ uuid.UUID) (*pgcommon.Pool, *sgbCuttingProxy) {
				return w10bCutPool(t, dsn, "name: ArchiveVersion")
			},
			wantStatus: http.StatusServiceUnavailable,
			wantType:   w10bProblemUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			wantDetail: "an upstream dependency is temporarily unavailable",
			wantCut:    true,
		},
		{
			name: "the pointer clear is refused by the database",
			txPool: func(t *testing.T, tenantID, userID uuid.UUID) (*pgcommon.Pool, *sgbCuttingProxy) {
				return w10bRestrictedPool(t, superPool, dsn, tenantID, userID, "REVOKE UPDATE ON workflow FROM {role}"), nil
			},
			wantStatus:   http.StatusInternalServerError,
			wantType:     w10bProblemInternal,
			wantCode:     "INTERNAL_ERROR",
			wantDetail:   "an unexpected error occurred",
			wantPointers: 1,
		},
		{
			name: "the connection is lost during the pointer clear",
			txPool: func(t *testing.T, _, _ uuid.UUID) (*pgcommon.Pool, *sgbCuttingProxy) {
				return w10bCutPool(t, dsn, "name: UpdateActiveVersion")
			},
			wantStatus:   http.StatusServiceUnavailable,
			wantType:     w10bProblemUnavailable,
			wantCode:     "UPSTREAM_UNAVAILABLE",
			wantDetail:   "an upstream dependency is temporarily unavailable",
			wantCut:      true,
			wantPointers: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			tenantID, userID := uuid.New(), uuid.New()
			wf, version := w7bSeedPublished(t, superPool, tenantID, userID, "w13a-archive-"+uuid.NewString())
			require.NoError(t, postgres.NewWorkflowRepo(superPool).UpdateActiveVersion(ctx, tenantID, wf.ID, &version.ID))
			txPool, proxy := tc.txPool(t, tenantID, userID)
			pointers := &w13aPointerCallCounter{WorkflowRepository: postgres.NewWorkflowRepo(superPool)}
			router := w10bEngine(handler.Services{Workflows: service.NewWorkflowService(service.WorkflowDeps{
				Transactor: postgres.NewTransactor(txPool),
				Workflows:  pointers,
				Versions:   postgres.NewWorkflowVersionRepo(superPool),
			})})
			path := "/api/v1/workflows/" + wf.ID.String() + "/archive"

			rec := w10bCall(router, http.MethodPost, path, tenantID, userID, "")

			require.Equal(t, tc.wantStatus, rec.Code, rec.Body.String())
			p := w10bDecodeProblem(t, rec)
			assert.Equal(t, tc.wantType, p.Type)
			assert.Equal(t, tc.wantStatus, p.Status)
			assert.Equal(t, tc.wantCode, p.Code)
			assert.Equal(t, tc.wantDetail, p.Detail, "the database's own message stays out of the response")
			assert.Equal(t, path, p.Instance)
			assert.NotContains(t, rec.Body.String(), "permission denied")
			if tc.wantCut {
				require.NotNil(t, proxy)
				assert.Equal(t, 1, proxy.cutCount(), "the failing statement is the one that lost its connection, and it is not retried")
			}
			assert.Equal(t, tc.wantPointers, pointers.calls.Load(),
				"the pointer is cleared only after the version archive succeeded")
			assert.Equal(t, "PUBLISHED", w10bReadVersion(t, superPool, version.ID).Status, "nothing stays archived")
			stored, err := postgres.NewWorkflowRepo(superPool).GetByID(ctx, tenantID, wf.ID)
			require.NoError(t, err)
			require.NotNil(t, stored.ActiveVersionID, "the workflow still points at its version")
			assert.Equal(t, version.ID, *stored.ActiveVersionID)
		})
	}
}
