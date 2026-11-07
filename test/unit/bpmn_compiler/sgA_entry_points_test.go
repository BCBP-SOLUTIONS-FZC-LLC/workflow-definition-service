package bpmn_compiler_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const sgADefinitionsOpen = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="D1" targetNamespace="http://bpmn.io/schema/bpmn">`

func sgAPool(id, calls string) string {
	return fmt.Sprintf(`  <bpmn:process id=%[1]q isExecutable="true">
    <bpmn:startEvent id="%[1]s_S"/>
    <bpmn:callActivity id="%[1]s_CA"><bpmn:extensionElements><zeebe:calledElement processId=%[2]q/></bpmn:extensionElements></bpmn:callActivity>
    <bpmn:endEvent id="%[1]s_E"/>
    <bpmn:sequenceFlow id="%[1]s_F1" sourceRef="%[1]s_S" targetRef="%[1]s_CA"/>
    <bpmn:sequenceFlow id="%[1]s_F2" sourceRef="%[1]s_CA" targetRef="%[1]s_E"/>
  </bpmn:process>
`, id, calls)
}

func TestEntryPoints_EmptyDocument_ReportsAMissingNamespaceNotMalformedXML(t *testing.T) {
	t.Parallel()
	c := bpmn_compiler.New()

	errs, err := c.Validate(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, errs, 1, "errors = %+v", errs)
	assert.Equal(t, domain.BPMNErrMissingNamespace, errs[0].Code)
	assert.Equal(t, domain.SeverityError, errs[0].Severity)
	assert.Contains(t, errs[0].Message, "BPMN namespace")

	_, err = c.CompileForPublish(context.Background(), "")
	var failed *domain.ValidationFailedError
	require.True(t, errors.As(err, &failed), "err = %v", err)
	require.Len(t, failed.Errors, 1)
	assert.Equal(t, domain.BPMNErrMissingNamespace, failed.Errors[0].Code)
	assert.False(t, errors.Is(err, domain.ErrMalformedBPMN))
}

func TestProcessID_ZeroOrSeveralProcesses_AreRefusedWithTheCount(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		bpmnXML   string
		wantCount string
	}{
		{"no process", sgADefinitionsOpen + "\n</bpmn:definitions>", "got 0"},
		{"two processes", sgADefinitionsOpen + "\n" + sgAPool("P1", "X") + sgAPool("P2", "X") + "</bpmn:definitions>", "got 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			id, err := bpmn_compiler.New().ProcessID(context.Background(), tc.bpmnXML)

			var failed *domain.ValidationFailedError
			require.True(t, errors.As(err, &failed), "err = %v", err)
			assert.Empty(t, id)
			require.Len(t, failed.Errors, 1)
			assert.Equal(t, domain.BPMNErrMultipleProcesses, failed.Errors[0].Code)
			assert.Equal(t, domain.SeverityError, failed.Errors[0].Severity)
			assert.Contains(t, failed.Errors[0].Message, tc.wantCount)
		})
	}

	id, err := bpmn_compiler.New().ProcessID(context.Background(), sgADefinitionsOpen+"\n"+sgAPool("P1", "X")+"</bpmn:definitions>")
	require.NoError(t, err)
	assert.Equal(t, "P1", id)
}

func TestModuleCalls_UnparsableDocument_HasNoCallsAndNoIssues(t *testing.T) {
	t.Parallel()
	calls, issues, err := bpmn_compiler.New().ModuleCalls(context.Background(), sgADefinitionsOpen+`<bpmn:process id="P1"`)

	require.NoError(t, err)
	assert.Nil(t, calls)
	assert.Nil(t, issues)
}

func TestModuleCalls_CancelledContext_ReturnsTheContextError(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	calls, issues, err := bpmn_compiler.New().ModuleCalls(ctx, sgADefinitionsOpen+"\n"+sgAPool("P1", "Process_Lib")+"</bpmn:definitions>")

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.Nil(t, calls)
	assert.Nil(t, issues)
}

func TestModuleCalls_OnlyAnUnresolvableCallAndARejectedElement_ReportsOnlyTheCall(t *testing.T) {
	t.Parallel()
	doc := sgADefinitionsOpen + "\n" + strings.Replace(sgAPool("P1", "=moduleName"), "</bpmn:process>", `<bpmn:scriptTask id="Script_1"/></bpmn:process>`, 1) + "</bpmn:definitions>"

	calls, issues, err := bpmn_compiler.New().ModuleCalls(context.Background(), doc)

	require.NoError(t, err)
	assert.Empty(t, calls)
	require.Len(t, issues, 1, "issues = %+v", issues)
	assert.Equal(t, domain.BPMNErrUnresolvedCalledElement, issues[0].Code)
	assert.Equal(t, "P1_CA", issues[0].NodeID)
}

func TestValidate_NoRunnableRoot_ReportsTheCallGraphAndTheRootAndNothingElse(t *testing.T) {
	t.Parallel()
	brokenFlow := strings.Replace(sgAPool("P1", "P2"), `targetRef="P1_E"`, `targetRef="Nowhere"`, 1)
	cases := []struct {
		name    string
		bpmnXML string
		wantAll []domain.BPMNErrorCode
	}{
		{
			name:    "pools calling each other leave no root",
			bpmnXML: sgADefinitionsOpen + "\n" + brokenFlow + sgAPool("P2", "P1") + "</bpmn:definitions>",
			wantAll: []domain.BPMNErrorCode{domain.BPMNErrCalledElementCycle, domain.BPMNErrNoRunnableProcess},
		},
		{
			name: "a single ignored process leaves no root",
			bpmnXML: sgADefinitionsOpen + "\n" + strings.Replace(brokenFlow, `<bpmn:startEvent id="P1_S"/>`,
				`<bpmn:extensionElements><zeebe:properties><zeebe:property name="ignore" value="true"/></zeebe:properties></bpmn:extensionElements><bpmn:startEvent id="P1_S"/>`, 1) + "</bpmn:definitions>",
			wantAll: []domain.BPMNErrorCode{domain.BPMNErrNoRunnableProcess},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := bpmn_compiler.New()

			errs, err := c.Validate(context.Background(), tc.bpmnXML)
			require.NoError(t, err)
			var got []domain.BPMNErrorCode
			for _, e := range errs {
				got = append(got, e.Code)
				assert.Equal(t, domain.SeverityError, e.Severity)
			}
			assert.Equal(t, tc.wantAll, got, "errors = %+v", errs)
			assert.Nil(t, findCode(errs, domain.BPMNErrInvalidSequenceFlowRef), "structural checks are skipped without a root")

			_, err = c.CompileForPublish(context.Background(), tc.bpmnXML)
			var failed *domain.ValidationFailedError
			require.True(t, errors.As(err, &failed), "err = %v", err)
			assert.Equal(t, errs, failed.Errors, "compile refuses with the same list Validate returns")
		})
	}

	withRoot, err := bpmn_compiler.New().Validate(context.Background(), sgADefinitionsOpen+"\n"+brokenFlow+"</bpmn:definitions>")
	require.NoError(t, err)
	assert.NotNil(t, findCode(withRoot, domain.BPMNErrInvalidSequenceFlowRef), "control: the same broken flow is reported once a root runs; errors = %+v", withRoot)
}
