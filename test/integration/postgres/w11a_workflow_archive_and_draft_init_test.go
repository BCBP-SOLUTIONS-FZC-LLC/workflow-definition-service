//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

func TestArchiveWorkflow_HTTP_PublishedWorkflow_ArchivesItsVersionClearsThePointerAndLeavesOtherTenantsAlone(t *testing.T) {
	w6cRegisterMetrics(t)
	pool := newTestPool(t)
	ctx := context.Background()
	tenantID, userID := uuid.New(), uuid.New()
	wf, version := w7bSeedPublished(t, pool, tenantID, userID, "w11a-archive")
	otherTenant := uuid.New()
	otherWf, otherVersion := w7bSeedPublished(t, pool, otherTenant, uuid.New(), "w11a-archive")
	workflows := postgres.NewWorkflowRepo(pool)
	require.NoError(t, workflows.UpdateActiveVersion(ctx, tenantID, wf.ID, &version.ID))
	require.NoError(t, workflows.UpdateActiveVersion(ctx, otherTenant, otherWf.ID, &otherVersion.ID))
	log := &w6cInfoLog{}
	router := w10bEngine(handler.Services{Workflows: service.NewWorkflowService(service.WorkflowDeps{
		Transactor: postgres.NewTransactor(pool),
		Workflows:  workflows,
		Versions:   postgres.NewWorkflowVersionRepo(pool),
		Log:        log,
	})})

	foreign := w10bCall(router, http.MethodPost, "/api/v1/workflows/"+wf.ID.String()+"/archive", otherTenant, uuid.New(), "")

	require.Equal(t, http.StatusNotFound, foreign.Code, foreign.Body.String())
	assert.Equal(t, "PUBLISHED", w10bReadVersion(t, pool, version.ID).Status, "another tenant cannot archive this workflow")
	untouched, err := workflows.GetByID(ctx, tenantID, wf.ID)
	require.NoError(t, err)
	require.NotNil(t, untouched.ActiveVersionID)
	assert.Equal(t, version.ID, *untouched.ActiveVersionID)

	okBefore := testutil.ToFloat64(observability.WFArchiveTotal.WithLabelValues("ok"))
	errBefore := testutil.ToFloat64(observability.WFArchiveTotal.WithLabelValues("err"))

	rec := w10bCall(router, http.MethodPost, "/api/v1/workflows/"+wf.ID.String()+"/archive", tenantID, userID, "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, map[string]any{
		"workflow_id": wf.ID.String(),
		"status":      "ARCHIVED",
		"message":     "Workflow archived successfully",
	}, body)
	assert.Equal(t, "ARCHIVED", w10bReadVersion(t, pool, version.ID).Status)
	stored, err := workflows.GetByID(ctx, tenantID, wf.ID)
	require.NoError(t, err)
	assert.Nil(t, stored.ActiveVersionID, "the workflow no longer points at a version")
	assert.Equal(t, "PUBLISHED", w10bReadVersion(t, pool, otherVersion.ID).Status, "the other tenant's version is untouched")
	otherStored, err := workflows.GetByID(ctx, otherTenant, otherWf.ID)
	require.NoError(t, err)
	require.NotNil(t, otherStored.ActiveVersionID)
	assert.Equal(t, otherVersion.ID, *otherStored.ActiveVersionID)
	assert.Equal(t, okBefore+1, testutil.ToFloat64(observability.WFArchiveTotal.WithLabelValues("ok")))
	assert.Equal(t, errBefore, testutil.ToFloat64(observability.WFArchiveTotal.WithLabelValues("err")))
	archived := log.named("workflow archived")
	require.Len(t, archived, 1)
	assert.Equal(t, map[string]any{
		"tenant_id": tenantID.String(), "user_id": userID.String(),
		"workflow_id": wf.ID.String(), "version_id": version.ID.String(),
	}, archived[0].fields)
}

func TestInitDraft_HTTP_WorkflowWithAnActiveVersion_StoresADraftCopyAndAnswersItsIdentity(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	tenantID, userID := uuid.New(), uuid.New()
	wf, version := w7bSeedPublished(t, pool, tenantID, userID, "w11a-init-draft")
	workflows := postgres.NewWorkflowRepo(pool)
	require.NoError(t, workflows.UpdateActiveVersion(ctx, tenantID, wf.ID, &version.ID))
	callerID := uuid.New()
	log := &w6cInfoLog{}
	router := w10bEngine(handler.Services{Drafts: service.NewDraftService(service.DraftDeps{
		Workflows: workflows,
		Versions:  postgres.NewWorkflowVersionRepo(pool),
		Log:       log,
	})})

	rec := w10bCall(router, http.MethodPost, "/api/v1/workflows/"+wf.ID.String()+"/draft", tenantID, callerID, "")

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	draftID, err := uuid.Parse(body["version_id"].(string))
	require.NoError(t, err)
	assert.NotEqual(t, version.ID, draftID)
	assert.Equal(t, map[string]any{
		"workflow_id":    wf.ID.String(),
		"version_id":     draftID.String(),
		"status":         "DRAFT",
		"version_number": nil,
		"message":        "Draft initialized",
	}, body)
	var (
		status        string
		numberIsNull  bool
		creator       uuid.UUID
		bpmn          string
		valid         bool
		recordVersion int64
		versionsOfWf  int
	)
	require.NoError(t, pool.WithConn(ctx, func(ctx context.Context, conn *pgxpool.Conn) error {
		if err := conn.QueryRow(ctx, `SELECT status::text, version_number IS NULL, created_by_user_id, bpmn_xml, is_valid, record_version
			FROM workflow_version WHERE id = $1 AND tenant_id = $2 AND workflow_id = $3`, draftID, tenantID, wf.ID).
			Scan(&status, &numberIsNull, &creator, &bpmn, &valid, &recordVersion); err != nil {
			return err
		}
		return conn.QueryRow(ctx, `SELECT count(*) FROM workflow_version WHERE workflow_id = $1`, wf.ID).Scan(&versionsOfWf)
	}))
	assert.Equal(t, "DRAFT", status)
	assert.True(t, numberIsNull, "a draft has no version number")
	assert.Equal(t, callerID, creator)
	assert.Equal(t, `<definitions id="w7b-source"/>`, bpmn, "the draft starts as a copy of the active version")
	assert.True(t, valid)
	assert.EqualValues(t, 1, recordVersion)
	assert.Equal(t, 2, versionsOfWf)
	assert.Equal(t, "PUBLISHED", w10bReadVersion(t, pool, version.ID).Status, "the active version is untouched")
	stored, err := workflows.GetByID(ctx, tenantID, wf.ID)
	require.NoError(t, err)
	require.NotNil(t, stored.ActiveVersionID)
	assert.Equal(t, version.ID, *stored.ActiveVersionID, "the workflow still points at the published version")
	initialised := log.named("draft initialized")
	require.Len(t, initialised, 1)
	assert.Equal(t, map[string]any{
		"tenant_id": tenantID.String(), "user_id": callerID.String(),
		"workflow_id": wf.ID.String(), "version_id": draftID.String(),
	}, initialised[0].fields)
}
