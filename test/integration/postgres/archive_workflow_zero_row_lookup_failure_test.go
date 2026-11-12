//go:build integration

package postgres_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func archiveZeroRowRouter(txPool, pool *pgcommon.Pool) *gin.Engine {
	return w10bEngine(handler.Services{Workflows: service.NewWorkflowService(service.WorkflowDeps{
		Transactor: postgres.NewTransactor(txPool),
		Workflows:  postgres.NewWorkflowRepo(pool),
		Versions:   postgres.NewWorkflowVersionRepo(pool),
	})})
}

// archiveZeroRowSeed stores a workflow whose active pointer names a version that
// is already archived, so the archive UPDATE finds no PUBLISHED row and the
// repository has to diagnose why with a second lookup.
func archiveZeroRowSeed(t *testing.T, superPool *pgcommon.Pool, tenantID, userID uuid.UUID) (workflowID, versionID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	wf, version := w7bSeedPublished(t, superPool, tenantID, userID, "archive-zero-row-"+uuid.NewString())
	require.NoError(t, postgres.NewWorkflowRepo(superPool).UpdateActiveVersion(ctx, tenantID, wf.ID, &version.ID))
	err := superPool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx, `UPDATE workflow_version SET status = 'ARCHIVED' WHERE id = $1`, version.ID)
		return err
	})
	require.NoError(t, err)
	return wf.ID, version.ID
}

func TestArchiveWorkflow_HTTP_TheLookupThatDiagnosesAZeroRowArchiveFails_Is500ForARefusedStatementAnd503ForALostConnection(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)

	cases := []struct {
		name       string
		txPool     func(t *testing.T, tenantID, userID uuid.UUID) (*pgcommon.Pool, *sgbCuttingProxy)
		wantStatus int
		wantType   string
		wantCode   string
		wantDetail string
		wantCut    bool
	}{
		{
			name: "the version lookup is refused by the database",
			txPool: func(t *testing.T, tenantID, userID uuid.UUID) (*pgcommon.Pool, *sgbCuttingProxy) {
				return w10bRestrictedPool(t, superPool, dsn, tenantID, userID,
					"REVOKE SELECT ON workflow_version FROM {role}",
					"GRANT SELECT (id, workflow_id, tenant_id, status, version_number) ON workflow_version TO {role}"), nil
			},
			wantStatus: http.StatusInternalServerError,
			wantType:   w10bProblemInternal,
			wantCode:   "INTERNAL_ERROR",
			wantDetail: "an unexpected error occurred",
		},
		{
			name: "the connection is lost during the version lookup",
			txPool: func(t *testing.T, _, _ uuid.UUID) (*pgcommon.Pool, *sgbCuttingProxy) {
				return w10bCutPool(t, dsn, "name: GetWorkflowVersionByID")
			},
			wantStatus: http.StatusServiceUnavailable,
			wantType:   w10bProblemUnavailable,
			wantCode:   "UPSTREAM_UNAVAILABLE",
			wantDetail: "an upstream dependency is temporarily unavailable",
			wantCut:    true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenantID, userID := uuid.New(), uuid.New()
			workflowID, versionID := archiveZeroRowSeed(t, superPool, tenantID, userID)
			txPool, proxy := tc.txPool(t, tenantID, userID)
			router := archiveZeroRowRouter(txPool, superPool)
			path := "/api/v1/workflows/" + workflowID.String() + "/archive"

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
				assert.Equal(t, 1, proxy.cutCount(), "the failing statement is the lookup that lost its connection, and it is not retried")
			}
			stored, err := postgres.NewWorkflowRepo(superPool).GetByID(context.Background(), tenantID, workflowID)
			require.NoError(t, err)
			require.NotNil(t, stored.ActiveVersionID, "the failed archive did not clear the active pointer")
			assert.Equal(t, versionID, *stored.ActiveVersionID)
		})
	}
}
