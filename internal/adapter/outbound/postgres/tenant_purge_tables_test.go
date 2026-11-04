package postgres

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTenantPurgeTables_NamesEveryPurgeStepInOrder(t *testing.T) {
	assert.Equal(t, []string{"workflow", "workflow_module", "workflow_template"}, TenantPurgeTables())
}
