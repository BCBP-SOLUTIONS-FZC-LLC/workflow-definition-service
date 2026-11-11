package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-connectors/v2/pkg/registry"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestConnectorService_Registry_ListsExactlyTheAuthorableConnectors(t *testing.T) {
	t.Parallel()

	defs := service.NewConnectorService(service.ConnectorDeps{}).Registry(context.Background())

	keys := make([]string, 0, len(defs))
	for k, d := range defs {
		keys = append(keys, k)
		assert.Equal(t, k, d.Type, "definition is keyed by its own type")
		assert.NotEmpty(t, d.DisplayName)
	}
	assert.ElementsMatch(t, []string{registry.TypeStorage, registry.TypeSendEmail, registry.TypeRestCall}, keys)
	for _, absent := range []string{registry.TypeChatNotify, registry.TypeSQLQuery, registry.TypeDocumentExtract} {
		assert.NotContains(t, defs, absent)
	}
	require.Contains(t, defs, registry.TypeStorage)
}
