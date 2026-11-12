package bpmn_compiler

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/validator"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func selfCallingModule(id string) string {
	return compOneDoc(`<bpmn:process id="` + id + `" isExecutable="true">
  <bpmn:startEvent id="` + id + `_Start"/>
  <bpmn:callActivity id="Call1">
    <bpmn:extensionElements><zeebe:calledElement processId="` + id + `"/></bpmn:extensionElements>
  </bpmn:callActivity>
  <bpmn:endEvent id="` + id + `_End"/>
  <bpmn:sequenceFlow id="F1" sourceRef="` + id + `_Start" targetRef="Call1"/>
  <bpmn:sequenceFlow id="F2" sourceRef="Call1" targetRef="` + id + `_End"/>
</bpmn:process>`)
}

func TestCompileModule_ProcessIDNotInTheFile_IsAnErrorNotAPanic(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		processID string
	}{
		{name: "an id the file does not define", processID: "NoSuchProcess"},
		{name: "an empty id", processID: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			xmlData := compOneDoc(compOneModuleProcess("ModProc", ""))

			var err error
			require.NotPanics(t, func() {
				_, _, err = New().compileModule(context.Background(), xmlData, tc.processID)
			})

			require.Error(t, err)
			assert.Contains(t, err.Error(), "not in the document")
		})
	}
}

func TestCompileModule_SelfCall_IsRefusedAsTheCallGraphValidatorJudgesIt(t *testing.T) {
	t.Parallel()

	xmlData := selfCallingModule("Loopy")

	defs, err := unmarshal(xmlData)
	require.NoError(t, err)
	graph := validator.ValidateCallGraph(defs)
	require.Len(t, graph, 1)
	require.Equal(t, domain.BPMNErrCalledElementCycle, graph[0].Code)

	_, _, err = New().compileModule(context.Background(), xmlData, "Loopy")

	var refused *domain.ValidationFailedError
	require.True(t, errors.As(err, &refused), "the element compiler must refuse the self-call, got %v", err)
	require.Len(t, refused.Errors, 1)
	assert.Equal(t, graph[0].Code, refused.Errors[0].Code)
	assert.Equal(t, graph[0].NodeID, refused.Errors[0].NodeID)
}
