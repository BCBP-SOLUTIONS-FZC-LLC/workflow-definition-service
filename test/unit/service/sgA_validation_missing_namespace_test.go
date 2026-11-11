package service_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port/mocks"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

const sgAWithoutBPMNNamespace = `<?xml version="1.0" encoding="UTF-8"?>
<definitions xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="Def_1" targetNamespace="http://bpmn.io/schema/bpmn">
  <process id="Process_1" isExecutable="true"><startEvent id="Start_1"/></process>
</definitions>`

func sgAWithoutZeebeNamespace() string {
	return strings.Replace(sgAWithoutBPMNNamespace, `xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"`, `xmlns="http://www.omg.org/spec/BPMN/20100524/MODEL"`, 1)
}

func TestValidationService_ValidateVersion_MissingNamespaceIsOneIssueAndCountsOnce(t *testing.T) {
	registerServiceMetrics(t)
	cases := []struct {
		name    string
		bpmnXML string
		status  domain.VersionStatus
		wantMsg string
	}{
		{"draft without the BPMN namespace", sgAWithoutBPMNNamespace, domain.VersionStatusDraft, "BPMN namespace"},
		{"draft without the Zeebe namespace", sgAWithoutZeebeNamespace(), domain.VersionStatusDraft, "Zeebe namespace"},
		{"published version without the BPMN namespace", sgAWithoutBPMNNamespace, domain.VersionStatusPublished, "BPMN namespace"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			modules := mocks.NewMockModuleVersionRepository(gomock.NewController(t))
			svc := service.NewValidationService(service.ValidationDeps{Compiler: bpmn_compiler.New(), ModuleVersions: modules})
			before := testutil.ToFloat64(observability.WFValidationFailuresTotal)

			isValid, errs, err := svc.ValidateVersion(context.Background(), uuid.New(),
				&domain.WorkflowVersion{Status: tc.status, BPMNXML: tc.bpmnXML})

			require.NoError(t, err)
			assert.False(t, isValid)
			require.Len(t, errs, 1, "errors = %+v", errs)
			assert.Equal(t, domain.BPMNErrMissingNamespace, errs[0].Code)
			assert.Equal(t, domain.SeverityError, errs[0].Severity)
			assert.Contains(t, errs[0].Message, tc.wantMsg)
			assert.Equal(t, before+1, testutil.ToFloat64(observability.WFValidationFailuresTotal))
		})
	}
}

func TestValidationService_ValidateVersion_ADocumentThatIsNotXMLIsAnErrorAndIsNotCounted(t *testing.T) {
	registerServiceMetrics(t)
	modules := mocks.NewMockModuleVersionRepository(gomock.NewController(t))
	svc := service.NewValidationService(service.ValidationDeps{Compiler: bpmn_compiler.New(), ModuleVersions: modules})
	before := testutil.ToFloat64(observability.WFValidationFailuresTotal)

	isValid, errs, err := svc.ValidateVersion(context.Background(), uuid.New(),
		&domain.WorkflowVersion{Status: domain.VersionStatusDraft, BPMNXML: `<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"><bpmn:process`})

	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrMalformedBPMN), "err = %v", err)
	assert.False(t, isValid)
	assert.Empty(t, errs)
	assert.Equal(t, before, testutil.ToFloat64(observability.WFValidationFailuresTotal))
}
