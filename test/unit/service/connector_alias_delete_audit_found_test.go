package service_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func TestConnectorService_DeleteRestAlias_AuditLineOnlyWhenTheAliasExisted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		found     bool
		wantLines int
	}{
		{"an alias that existed is audited", true, 1},
		{"an alias that did not exist writes no audit line", false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			log := &sgCAuditLog{}
			repo := &sgCAliasRepo{found: tt.found}
			svc := service.NewConnectorService(service.ConnectorDeps{Aliases: repo, Log: log})

			found, err := svc.DeleteRestAlias(context.Background(), "missing-alias")

			require.NoError(t, err)
			assert.Equal(t, tt.found, found)
			lines := log.snapshot()
			require.Len(t, lines, tt.wantLines)
			if tt.found {
				assert.Equal(t, "connector alias deleted", lines[0].msg)
			}
		})
	}
}
