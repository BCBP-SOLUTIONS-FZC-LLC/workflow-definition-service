//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func sgCSeedStarter(t *testing.T, pool *pgcommon.Pool, id uuid.UUID, tenantID *uuid.UUID, name string, createdAt time.Time) {
	t.Helper()
	scope := "global"
	if tenantID != nil {
		scope = "tenant"
	}
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		_, err := conn.Exec(ctx,
			`INSERT INTO workflow_template (id, tenant_id, scope, name, bpmn_xml, created_by_user_id, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, '<bpmn/>', $5, $6, $6)`,
			id, tenantID, scope, name, uuid.New(), createdAt)
		return err
	})
	require.NoError(t, err)
}

func sgCListStartersBody(t *testing.T, w *httptest.ResponseRecorder) (starters []map[string]any, pagination map[string]any) {
	t.Helper()
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body struct {
		Starters   []map[string]any `json:"starters"`
		Pagination map[string]any   `json:"pagination"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	return body.Starters, body.Pagination
}

func TestStarterList_NoQueryParams_ReturnsDefaultPagingNewestFirstAndOmitsInternalFields(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	tenantID, userID := uuid.New(), uuid.New()
	tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	oldestGlobal, middleOwn, newestGlobal := uuid.New(), uuid.New(), uuid.New()
	sgCSeedStarter(t, superPool, oldestGlobal, nil, "oldest global", base)
	sgCSeedStarter(t, superPool, middleOwn, &tenantID, "middle own", base.Add(time.Hour))
	sgCSeedStarter(t, superPool, newestGlobal, nil, "newest global", base.Add(2*time.Hour))
	router := sgCStarterRouter(tenantPool, systemPool, &sgCInfoLog{})

	w := httptest.NewRecorder()
	router.ServeHTTP(w, sgCStarterRequest(http.MethodGet, "/api/v1/starters", tenantID, userID))

	starters, pagination := sgCListStartersBody(t, w)
	require.Len(t, starters, 3)
	assert.Equal(t, []any{newestGlobal.String(), middleOwn.String(), oldestGlobal.String()},
		[]any{starters[0]["id"], starters[1]["id"], starters[2]["id"]}, "newest first")
	assert.EqualValues(t, 3, pagination["total_count"])
	assert.EqualValues(t, 1, pagination["page"])
	assert.EqualValues(t, 20, pagination["limit"])

	assert.NotContains(t, starters[0], "tenant_id", "a global starter has no tenant_id key")
	assert.NotContains(t, starters[2], "tenant_id", "a global starter has no tenant_id key")
	assert.Equal(t, tenantID.String(), starters[1]["tenant_id"])
	assert.Equal(t, "tenant", starters[1]["scope"])
	assert.Equal(t, "global", starters[0]["scope"])
}

func TestStarterList_OtherTenantsStarters_AreNeverListedOrCounted(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	tenantA, userA := uuid.New(), uuid.New()
	tenantB := uuid.New()
	tenantPoolA := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantA, &userA)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	ownID, otherID, globalID := uuid.New(), uuid.New(), uuid.New()
	sgCSeedStarter(t, superPool, ownID, &tenantA, "tenant A starter", base)
	sgCSeedStarter(t, superPool, otherID, &tenantB, "tenant B starter", base.Add(time.Hour))
	sgCSeedStarter(t, superPool, globalID, nil, "global starter", base.Add(2*time.Hour))

	t.Run("caller sees its own and global starters only", func(t *testing.T) {
		router := sgCStarterRouter(tenantPoolA, systemPool, &sgCInfoLog{})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, sgCStarterRequest(http.MethodGet, "/api/v1/starters", tenantA, userA))

		starters, pagination := sgCListStartersBody(t, w)
		ids := make([]any, len(starters))
		for i, s := range starters {
			ids[i] = s["id"]
		}
		assert.Equal(t, []any{globalID.String(), ownID.String()}, ids)
		assert.EqualValues(t, 2, pagination["total_count"], "the count excludes the other tenant's starter")
	})

	t.Run("a pool bound to A asked with B's id lists no tenant B or tenant A row", func(t *testing.T) {
		repo := postgres.NewStarterRepo(tenantPoolA)

		results, total, err := repo.List(context.Background(), tenantB, port.StarterFilter{Page: 1, Limit: 20})

		require.NoError(t, err)
		assert.EqualValues(t, 1, total)
		require.Len(t, results, 1)
		assert.Equal(t, globalID, results[0].ID)
	})
}
