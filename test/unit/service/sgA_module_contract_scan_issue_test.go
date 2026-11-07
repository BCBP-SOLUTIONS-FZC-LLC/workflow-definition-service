package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestModuleContract_ScanIssueOnTheModulesOwnDocument_RefusesWithTheIssueAsIs(t *testing.T) {
	t.Parallel()
	withScript := strings.Replace(contractPackageBPMN, "</bpmn:process>", `<bpmn:scriptTask id="Pkg_Script" name="Run"/></bpmn:process>`, 1)
	v := libraryVersion(uuid.New(), "Process_Package", 3, withScript)
	svc, tenantID := contractService(t, domain.ScopeTenant, v, checkLibrary)

	got, err := svc.Contract(context.Background(), tenantID, v.ModuleID, v.ID)

	var failed *domain.ValidationFailedError
	require.True(t, errors.As(err, &failed), "err = %v", err)
	assert.Nil(t, got)
	require.Len(t, failed.Errors, 1, "errors = %+v", failed.Errors)
	assert.Equal(t, domain.BPMNErrRejectedElement, failed.Errors[0].Code)
	assert.Equal(t, "Pkg_Script", failed.Errors[0].NodeID)
	assert.Equal(t, domain.SeverityError, failed.Errors[0].Severity)
	assert.NotContains(t, failed.Errors[0].Message, "inside ", "an issue on the module's own document is not attributed to a call")
}

func TestModuleContract_SameDocumentWithoutTheRejectedElement_ReadsItsContract(t *testing.T) {
	t.Parallel()
	v := libraryVersion(uuid.New(), "Process_Package", 3, contractPackageBPMN)
	svc, tenantID := contractService(t, domain.ScopeTenant, v, checkLibrary)

	got, err := svc.Contract(context.Background(), tenantID, v.ModuleID, v.ID)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Len(t, got.Tasks, 3)
}
