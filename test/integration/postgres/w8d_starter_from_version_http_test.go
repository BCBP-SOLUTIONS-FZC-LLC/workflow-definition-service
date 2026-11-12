//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

type w8dStarterRow struct {
	name          string
	descriptionOK bool
	categoryOK    bool
	tenantID      *uuid.UUID
	createdBy     *uuid.UUID
	sourceVersion *uuid.UUID
	bpmnXML       string
	scope         string
}

func w8dReadStarterRow(t *testing.T, pool *pgcommon.Pool, id uuid.UUID) w8dStarterRow {
	t.Helper()
	var row w8dStarterRow
	var description, category *string
	err := pool.WithConn(context.Background(), func(ctx context.Context, conn *pgxpool.Conn) error {
		return conn.QueryRow(ctx,
			`SELECT name, description, category, tenant_id, created_by_user_id, source_workflow_version_id, bpmn_xml, scope::text
			   FROM workflow_template WHERE id = $1`, id).
			Scan(&row.name, &description, &category, &row.tenantID, &row.createdBy, &row.sourceVersion, &row.bpmnXML, &row.scope)
	})
	require.NoError(t, err)
	row.descriptionOK = description != nil
	row.categoryOK = category != nil
	return row
}

func TestStarterFromWorkflowVersion_HTTP_OnlyNameSent_StoresNullsAndAnswersEmptyDescriptionWithoutCategory(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	tenantID, userID := uuid.New(), uuid.New()
	tenantPool := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantID, &userID)
	systemPool := fixtures.NewRestrictedRolePool(t, dsn, true, nil, nil)
	wf := seedWorkflow(t, superPool, tenantID, "starter-source")
	version := seedDraft(t, superPool, wf, "<source-bpmn/>")
	publishVersion(t, superPool, version, 1)

	svc := service.NewStarterService(service.StarterDeps{
		Starters:       postgres.NewStarterRepo(tenantPool),
		GlobalStarters: postgres.NewStarterRepo(systemPool),
		Versions:       postgres.NewWorkflowVersionRepo(tenantPool),
	})
	gin.SetMode(gin.TestMode)
	router := gin.New()
	for _, mw := range gincommon.ProtectedMiddlewares(gincommon.Config{}) {
		router.Use(mw)
	}
	router.Use(httpmiddleware.RequireIdentity(nil))
	httpadapter.RegisterAPIRoutes(router.Group("/api/v1"), handler.New(handler.Services{Starters: svc}))

	r := httptest.NewRequest(http.MethodPost, "/api/v1/starters/from-workflow-version/"+version.ID.String(),
		bytes.NewReader([]byte(`{"name":"Onboarding"}`)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", tenantID.String())
	r.Header.Set("x-user-id", userID.String())
	r.Header.Set("x-tenant-roles", "tenant_admin")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)

	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	description, hasDescription := body["description"]
	assert.True(t, hasDescription, "the description key is always present")
	assert.Equal(t, "", description)
	assert.NotContains(t, body, "category")
	assert.Equal(t, "Onboarding", body["name"])
	id, err := uuid.Parse(body["id"].(string))
	require.NoError(t, err)

	row := w8dReadStarterRow(t, superPool, id)
	assert.False(t, row.descriptionOK, "an empty description is stored as NULL")
	assert.False(t, row.categoryOK, "an empty category is stored as NULL")
	assert.Equal(t, "Onboarding", row.name)
	assert.Equal(t, "tenant", row.scope)
	assert.Equal(t, "<source-bpmn/>", row.bpmnXML)
	require.NotNil(t, row.tenantID)
	assert.Equal(t, tenantID, *row.tenantID)
	require.NotNil(t, row.createdBy)
	assert.Equal(t, userID, *row.createdBy)
	require.NotNil(t, row.sourceVersion)
	assert.Equal(t, version.ID, *row.sourceVersion)
}
