//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

func TestVersionReads_StoredModuleRefsThatCannotBeDecoded_Answer500NotAnUnresolvedModule(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	tenantID, userID := uuid.New(), uuid.New()
	processID := w8cProcessID()
	w8cSeedPublishedModule(t, pool, &tenantID, processID)
	module, err := pgadapter.NewModuleVersionRepo(pool).Resolve(ctx, tenantID, processID, 0)
	require.NoError(t, err)
	wf := seedWorkflow(t, pool, tenantID, "w10a-damaged-refs")
	draft := seedDraft(t, pool, wf, strings.ReplaceAll(w8cCallerBPMN, "%PROCESS%", processID))
	read, err := pgadapter.NewWorkflowVersionRepo(pool).GetByID(ctx, tenantID, draft.ID)
	require.NoError(t, err)
	refs := []domain.ModuleRef{{ModuleID: module.ModuleID, VersionID: module.ID, ProcessID: processID, VersionNumber: *module.VersionNumber}}
	require.NoError(t, pgadapter.NewWorkflowVersionRepo(pool).Publish(ctx, tenantID, draft.ID, read.RecordVersion, 1, `{}`, "w10a-hash", refs))
	intact, err := json.Marshal(refs)
	require.NoError(t, err)
	router := w7cRouter(pool)
	versionPath := "/api/v1/workflows/" + wf.ID.String() + "/versions/" + draft.ID.String()

	get := func(path string) (int, map[string]any, string) {
		w := w7cCall(router, http.MethodGet, path, tenantID, userID)
		var body map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body, w.Body.String()
	}
	assertInternal := func(t *testing.T, path string) {
		t.Helper()
		status, body, raw := get(path)
		require.Equal(t, http.StatusInternalServerError, status, raw)
		assert.Equal(t, "INTERNAL_ERROR", body["code"])
		assert.Equal(t, "https://api.workflow.platform/errors/internal-error", body["type"])
		assert.Equal(t, "an unexpected error occurred", body["detail"])
		assert.NotContains(t, raw, "module refs", "the decode error is not shown to the caller")
		assert.NotContains(t, raw, "is_valid")
	}

	t.Run("intact references read back and the snapshot validates", func(t *testing.T) {
		status, body, raw := get(versionPath)
		require.Equal(t, http.StatusOK, status, raw)
		listed, ok := body["module_refs"].([]any)
		require.True(t, ok, raw)
		require.Len(t, listed, 1)
		assert.Equal(t, processID, listed[0].(map[string]any)["process_id"])

		status, body, raw = get(versionPath + "/validation")
		require.Equal(t, http.StatusOK, status, raw)
		assert.Equal(t, true, body["is_valid"], raw)
	})

	t.Run("a version with no references reports the module call as unresolved", func(t *testing.T) {
		damageModuleRefs(t, pool, draft.ID, `[]`)

		status, body, raw := get(versionPath + "/validation")

		require.Equal(t, http.StatusOK, status, raw)
		assert.Equal(t, false, body["is_valid"])
		assert.Contains(t, raw, "UNRESOLVED_CALLED_ELEMENT")
	})

	for name, damaged := range map[string]string{
		"an object instead of a list": `{"module_id":"not-a-list"}`,
		"a list of numbers":           `[1,2]`,
		"a version id that is no id":  `[{"module_id":"` + uuid.NewString() + `","version_id":"not-an-id","process_id":"P","version_number":1}]`,
	} {
		t.Run("references stored as "+name+" answer 500 on the version read and the validation", func(t *testing.T) {
			damageModuleRefs(t, pool, draft.ID, damaged)

			assertInternal(t, versionPath)
			assertInternal(t, versionPath+"/validation")
		})
	}

	t.Run("repairing the references restores both reads", func(t *testing.T) {
		damageModuleRefs(t, pool, draft.ID, string(intact))

		status, _, raw := get(versionPath)
		require.Equal(t, http.StatusOK, status, raw)
		status, body, raw := get(versionPath + "/validation")
		require.Equal(t, http.StatusOK, status, raw)
		assert.Equal(t, true, body["is_valid"])
	})
}

func TestConnectorAliasWrite_DatabaseUnavailable_Answers503AndStoresNothing(t *testing.T) {
	livePool, dsn := fixtures.NewTestPoolAndDSN(t)
	ctx := context.Background()
	alias := domain.ConnectorRestAlias{
		Alias: "w10a-alias", Method: "GET", BaseURL: "http://tender-service.internal",
		PathTemplate: "/w10a/x", Timeout: 3 * time.Second,
	}
	body := map[string]any{
		"alias": alias.Alias, "method": alias.Method, "baseURL": alias.BaseURL,
		"pathTemplate": alias.PathTemplate, "timeoutMs": 3000,
	}

	t.Run("the pool is closed", func(t *testing.T) {
		closed := w10aClosedPool(t, dsn)

		w := aliasWriteThroughHTTP(t, closed, body)

		require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
		assert.Equal(t, "application/problem+json", w.Header().Get("Content-Type"))
		var problem w10aProblem
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &problem))
		assert.Equal(t, "UPSTREAM_UNAVAILABLE", problem.Code)
		assert.Equal(t, "https://api.workflow.platform/errors/service-unavailable", problem.Type)
		assert.Equal(t, "an upstream dependency is temporarily unavailable", problem.Detail)
		stored, err := pgadapter.NewConnectorAliasRepo(livePool).ListRest(ctx)
		require.NoError(t, err)
		assert.Empty(t, stored, "the refused write left no alias")
	})

	t.Run("the repository wraps a closed pool and a cancelled context as unavailable", func(t *testing.T) {
		closedErr := pgadapter.NewConnectorAliasRepo(w10aClosedPool(t, dsn)).UpsertRest(ctx, alias)
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		cancelledErr := pgadapter.NewConnectorAliasRepo(livePool).UpsertRest(cancelled, alias)

		require.ErrorIs(t, closedErr, domain.ErrUpstreamUnavailable)
		require.ErrorIs(t, cancelledErr, domain.ErrUpstreamUnavailable)
		assert.ErrorIs(t, cancelledErr, context.Canceled)
	})

	t.Run("the same request on the live pool stores the alias", func(t *testing.T) {
		w := aliasWriteThroughHTTP(t, livePool, body)

		require.Equal(t, http.StatusNoContent, w.Code, w.Body.String())
		stored, err := pgadapter.NewConnectorAliasRepo(livePool).ListRest(ctx)
		require.NoError(t, err)
		assert.Equal(t, []domain.ConnectorRestAlias{alias}, stored)
	})
}
