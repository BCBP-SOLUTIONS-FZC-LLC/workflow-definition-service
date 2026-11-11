//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-gincommon/v2/pkg/gincommon"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	httpmiddleware "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/middleware"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

type sgCInfoLine struct {
	msg    string
	fields map[string]any
}

type sgCInfoLog struct {
	mu    sync.Mutex
	infos []sgCInfoLine
}

func (l *sgCInfoLog) Info(msg string, fields map[string]any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.infos = append(l.infos, sgCInfoLine{msg: msg, fields: fields})
}
func (*sgCInfoLog) Debug(string, map[string]any) {}
func (*sgCInfoLog) Warn(string, map[string]any)  {}
func (*sgCInfoLog) Error(string, map[string]any) {}
func (*sgCInfoLog) Fatal(string, map[string]any) {}

func (l *sgCInfoLog) lines(msg string) []sgCInfoLine {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []sgCInfoLine
	for _, line := range l.infos {
		if line.msg == msg {
			out = append(out, line)
		}
	}
	return out
}

func sgCStarterRouter(tenantPool, systemPool *pgcommon.Pool, log *sgCInfoLog) *gin.Engine {
	svc := service.NewStarterService(service.StarterDeps{
		Starters:       postgres.NewStarterRepo(tenantPool),
		GlobalStarters: postgres.NewStarterRepo(systemPool),
		Log:            log,
	})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		r.Use(mw)
	}
	r.Use(httpmiddleware.RequireIdentity(nil))
	httpadapter.RegisterAPIRoutes(r.Group("/api/v1"), handler.New(handler.Services{Starters: svc}))
	return r
}

func sgCStarterRequest(method, path string, tenantID, userID uuid.UUID) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", tenantID.String())
	r.Header.Set("x-user-id", userID.String())
	r.Header.Set("x-tenant-roles", "tenant_admin")
	return r
}

func sgCStarterRows(t *testing.T, pool *pgcommon.Pool, id uuid.UUID) int {
	t.Helper()
	var count int
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx, `SELECT count(*) FROM workflow_template WHERE id = $1`, id).Scan(&count)
	})
	require.NoError(t, err)
	return count
}

func TestStarterDelete_TenantAdminDeletesOwnTenantStarter_RemovesRowAndLogsIt(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	tenantID, userID := uuid.New(), uuid.New()
	otherTenantID := uuid.New()
	tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)

	ownID, otherID := uuid.New(), uuid.New()
	insertStarter(t, dsn, ownID, "tenant", &tenantID)
	insertStarter(t, dsn, otherID, "tenant", &otherTenantID)
	log := &sgCInfoLog{}
	router := sgCStarterRouter(tenantPool, systemPool, log)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, sgCStarterRequest(http.MethodDelete, "/api/v1/starters/"+ownID.String(), tenantID, userID))

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	assert.Equal(t, map[string]any{"starter_id": ownID.String(), "message": "Starter deleted"}, body)

	assert.Equal(t, 0, sgCStarterRows(t, superPool, ownID), "the row is gone from workflow_template")
	assert.Equal(t, 1, sgCStarterRows(t, superPool, otherID), "another tenant's starter is untouched")

	get := httptest.NewRecorder()
	router.ServeHTTP(get, sgCStarterRequest(http.MethodGet, "/api/v1/starters/"+ownID.String(), tenantID, userID))
	require.Equal(t, http.StatusNotFound, get.Code, get.Body.String())
	var problem map[string]any
	require.NoError(t, json.Unmarshal(get.Body.Bytes(), &problem))
	assert.Equal(t, "NOT_FOUND", problem["code"])

	lines := log.lines("starter deleted")
	require.Len(t, lines, 1)
	assert.Equal(t, tenantID.String(), lines[0].fields["tenant_id"])
	assert.Equal(t, ownID.String(), lines[0].fields["starter_id"])
}

func TestStarterDelete_OtherTenantsStarter_IsNotFoundAndKeepsTheRow(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	callerTenant, userID := uuid.New(), uuid.New()
	ownerTenant := uuid.New()
	tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &callerTenant, &userID)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)

	foreignID := uuid.New()
	insertStarter(t, dsn, foreignID, "tenant", &ownerTenant)
	log := &sgCInfoLog{}
	router := sgCStarterRouter(tenantPool, systemPool, log)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, sgCStarterRequest(http.MethodDelete, "/api/v1/starters/"+foreignID.String(), callerTenant, userID))

	require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
	var problem map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &problem))
	assert.Equal(t, "NOT_FOUND", problem["code"])
	assert.Equal(t, 1, sgCStarterRows(t, superPool, foreignID))
	assert.Empty(t, log.lines("starter deleted"))

	asOwner := service.NewStarterService(service.StarterDeps{
		Starters:       postgres.NewStarterRepo(tenantPool),
		GlobalStarters: postgres.NewStarterRepo(systemPool),
	})
	err := asOwner.Delete(context.Background(), ownerTenant, foreignID)
	require.ErrorIs(t, err, domain.ErrNotFound, "the query names the owner, row-level security still hides the row from the caller's pool")
	assert.Equal(t, 1, sgCStarterRows(t, superPool, foreignID))
}
