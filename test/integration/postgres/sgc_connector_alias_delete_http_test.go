//go:build integration

package postgres_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestConnectorAliasDelete_AliasDiffersOnlyByCase_IsNotFoundAndKeepsTheStoredRow(t *testing.T) {
	pool := newTestPool(t)
	suffix := uuid.NewString()
	stored := "alias-" + suffix
	sgCAliasSeed(t, pool, domain.ConnectorRestAlias{
		Alias: stored, Method: "GET", BaseURL: "http://tender-service.internal",
		PathTemplate: "/x", Timeout: 5 * time.Second,
	})

	upper := sgCAliasServe(t, pool, http.MethodDelete, "/api/v1/internal/connector-aliases/rest/ALIAS-"+suffix)

	require.Equal(t, http.StatusNotFound, upper.Code, upper.Body.String())
	var prob map[string]any
	require.NoError(t, json.Unmarshal(upper.Body.Bytes(), &prob))
	assert.Equal(t, "NOT_FOUND", prob["code"])
	assert.Equal(t, "alias not found", prob["detail"])

	listed, err := postgres.NewConnectorAliasRepo(pool).ListRest(t.Context())
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, stored, listed[0].Alias)

	exact := sgCAliasServe(t, pool, http.MethodDelete, "/api/v1/internal/connector-aliases/rest/"+stored)
	require.Equal(t, http.StatusNoContent, exact.Code, exact.Body.String())
	after, err := postgres.NewConnectorAliasRepo(pool).ListRest(t.Context())
	require.NoError(t, err)
	assert.Empty(t, after)
}
