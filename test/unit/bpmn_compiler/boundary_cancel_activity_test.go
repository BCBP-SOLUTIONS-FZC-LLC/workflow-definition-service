package bpmn_compiler_test

import (
	"context"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/v2/pkg/dsl"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler"
)

func withCancelActivity(doc, attachedTo, value string) string {
	marker := `attachedToRef="` + attachedTo + `"`
	if value == "" {
		return doc
	}
	return strings.Replace(doc, marker, marker+` cancelActivity="`+value+`"`, 1)
}

func subProcessBoundaryDoc(kind string) string {
	doc := strings.Replace(subprocessTimerBoundaryBPMN, ` cancelActivity="true"`, "", 1)
	if kind == "message" {
		doc = strings.Replace(doc, `<bpmn:timerEventDefinition>
        <bpmn:timeDuration>72h</bpmn:timeDuration>
      </bpmn:timerEventDefinition>`, `<bpmn:messageEventDefinition messageRef="Msg_x"/>`, 1)
		doc = strings.Replace(doc, `<bpmn:process id="Proc_1"`, `<bpmn:message id="Msg_x" name="x"/>
  <bpmn:process id="Proc_1"`, 1)
	}
	return doc
}

func subProcessInterrupting(t *testing.T, kind, value string) bool {
	t.Helper()
	plan, err := bpmn_compiler.New().Compile(context.Background(), withCancelActivity(subProcessBoundaryDoc(kind), "SubProcess_1", value))
	require.NoError(t, err)
	for _, step := range plan.Execution.Steps {
		if step.SubWorkflow == nil {
			continue
		}
		if kind == "timer" {
			require.Len(t, step.SubWorkflow.TimerPaths, 1)
			return step.SubWorkflow.TimerPaths[0].Interrupting
		}
		require.Len(t, step.SubWorkflow.MessagePaths, 1)
		return step.SubWorkflow.MessagePaths[0].Interrupting
	}
	t.Fatal("no sub-process step in the plan")
	return false
}

func callActivityInterrupting(t *testing.T, kind, value string) bool {
	t.Helper()
	doc := mainWithCalls([]callSite{
		{ID: "CA_1", Lane: "Engineering", Calls: "Process_M", Boundaries: []string{kind}},
	}, moduleWithTask("Process_M", validCandidateUser))
	call := callSteps(planNamed(t, compileForPublish(t, withCancelActivity(doc, "CA_1", value)), "Main"))[0]
	if kind == "timer" {
		require.Len(t, call.TimerPaths, 1)
		return call.TimerPaths[0].Interrupting
	}
	require.Len(t, call.MessagePaths, 1)
	return call.MessagePaths[0].Interrupting
}

func userTaskTimerInterrupting(t *testing.T, value string) bool {
	t.Helper()
	doc := strings.Replace(timerBoundaryNIBPMN, `cancelActivity="false"`, `cancelActivity="`+value+`"`, 1)
	plan, err := bpmn_compiler.New().Compile(context.Background(), doc)
	require.NoError(t, err)
	var timer *dsl.BoundaryTimer
	for _, d := range plan.Departments {
		for _, s := range d.Stages {
			if s.BoundaryTimer != nil {
				timer = s.BoundaryTimer
			}
		}
	}
	require.NotNil(t, timer)
	return timer.Interrupting
}

func TestCompile_BoundaryCancelActivity_IsReadTheSameOnEveryHost(t *testing.T) {
	values := []struct {
		value string
		want  bool
	}{
		{value: "", want: true},
		{value: "true", want: true},
		{value: "1", want: true},
		{value: "false", want: false},
		{value: "0", want: false},
	}
	hosts := []struct {
		name string
		read func(t *testing.T, kind, value string) bool
		kind []string
	}{
		{name: "sub-process", read: subProcessInterrupting, kind: []string{"timer", "message"}},
		{name: "call activity", read: callActivityInterrupting, kind: []string{"timer", "message"}},
		{
			name: "user task",
			read: func(t *testing.T, _, value string) bool { return userTaskTimerInterrupting(t, value) },
			kind: []string{"timer"},
		},
	}
	for _, h := range hosts {
		for _, kind := range h.kind {
			for _, v := range values {
				if h.name == "user task" && v.value == "" {
					continue
				}
				t.Run(h.name+"/"+kind+"/cancelActivity="+v.value, func(t *testing.T) {
					require.Equal(t, v.want, h.read(t, kind, v.value))
				})
			}
		}
	}
}
