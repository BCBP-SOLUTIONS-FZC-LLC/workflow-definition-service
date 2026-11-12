package postgres

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres/db"
)

func versionRowWithModuleRefs(raw string) db.WorkflowVersion {
	row := db.WorkflowVersion{
		ID: uuid.New(), WorkflowID: uuid.New(), TenantID: uuid.New(),
		Status: db.WorkflowVersionStatusPUBLISHED, BpmnXml: "<bpmn/>", CreatedByUserID: uuid.New(),
	}
	if raw != "" {
		row.ModuleRefs = []byte(raw)
	}
	return row
}

func TestWorkflowVersionFromDB_DamagedModuleRefs_ReturnTheDecodeError(t *testing.T) {
	for name, raw := range map[string]string{
		"an object instead of a list": `{"module_id":"x"}`,
		"a list of numbers":           `[1,2]`,
		"a bad version id":            `[{"module_id":"` + uuid.NewString() + `","version_id":"not-a-uuid","process_id":"P","version_number":1}]`,
	} {
		t.Run(name, func(t *testing.T) {
			v, err := workflowVersionFromDB(versionRowWithModuleRefs(raw))

			require.Error(t, err)
			assert.Contains(t, err.Error(), "module refs")
			assert.Nil(t, v)
		})
	}
}

func TestWorkflowVersionFromDB_ModuleRefs_AbsentAndNullMeanNoModules(t *testing.T) {
	for name, raw := range map[string]string{"column not set": "", "json null": "null", "empty list": "[]"} {
		t.Run(name, func(t *testing.T) {
			v, err := workflowVersionFromDB(versionRowWithModuleRefs(raw))

			require.NoError(t, err)
			assert.Empty(t, v.ModuleRefs)
		})
	}
}

func TestWorkflowVersionFromDB_ModuleRefs_AreDecoded(t *testing.T) {
	moduleID, versionID := uuid.New(), uuid.New()
	raw := `[{"module_id":"` + moduleID.String() + `","version_id":"` + versionID.String() + `","process_id":"Process_M","version_number":4}]`

	v, err := workflowVersionFromDB(versionRowWithModuleRefs(raw))

	require.NoError(t, err)
	require.Len(t, v.ModuleRefs, 1)
	assert.Equal(t, moduleID, v.ModuleRefs[0].ModuleID)
	assert.Equal(t, versionID, v.ModuleRefs[0].VersionID)
	assert.Equal(t, "Process_M", v.ModuleRefs[0].ProcessID)
	assert.Equal(t, int32(4), v.ModuleRefs[0].VersionNumber)
}
