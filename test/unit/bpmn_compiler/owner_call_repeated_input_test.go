package bpmn_compiler_test

import (
	"fmt"
	"html"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func callWithInputs(id, processID string, inputs ...string) string {
	var in strings.Builder
	for _, i := range inputs {
		in.WriteString(i)
	}
	return fmt.Sprintf("    <bpmn:callActivity id=%q><bpmn:extensionElements><zeebe:calledElement processId=%q/><zeebe:ioMapping>%s</zeebe:ioMapping></bpmn:extensionElements></bpmn:callActivity>\n", id, processID, in.String())
}

func callInput(target, source string) string {
	return `<zeebe:input source="` + html.EscapeString(source) + `" target="` + html.EscapeString(target) + `"/>`
}

func callDocument(call string) string {
	body := "    <bpmn:startEvent id=\"S\"/>\n" + call + "    <bpmn:endEvent id=\"E\"/>\n" + flowXML("F1", "S", "C") + flowXML("F2", "C", "E")
	return flowDocument(oneLane("S", "C", "E"), body, lanedModule("Mod", moduleLane{"Rev", "", "M_T"}))
}

func assertRepeatedInput(t *testing.T, errs []domain.BPMNValidationError) {
	t.Helper()
	for _, e := range errs {
		if e.Code == domain.BPMNErrInvalidZeebeProperty && e.NodeID == "C" && strings.Contains(e.Message, "more than once") {
			return
		}
	}
	t.Errorf("errors = %+v, want INVALID_ZEEBE_PROPERTY on C saying an input is given more than once", errs)
}

func TestValidate_CallActivityRepeatingAnInput_IsRefusedOnTheCall(t *testing.T) {
	depts := `{"Rev":"Engineering"}`
	assignees := fmt.Sprintf(`{"M_T":%q}`, validCandidateUser)
	cases := map[string][]string{
		"Depts":                          {callInput("Depts", depts), callInput("Depts", depts)},
		"Assignees":                      {callInput("Assignees", assignees), callInput("Assignees", assignees)},
		"dept_id":                        {callInput("dept_id", "engineering"), callInput("dept_id", "engineering")},
		"Depts, a malformed value first": {callInput("Depts", "not json"), callInput("Depts", depts)},
	}
	for name, inputs := range cases {
		t.Run(name, func(t *testing.T) {
			doc := callDocument(callWithInputs("C", "Mod", inputs...))

			assertRepeatedInput(t, validateDocument(t, doc))
			assertRepeatedInput(t, validateForPublishDocument(t, doc))
		})
	}
}

func TestValidate_CallActivityWithEachInputOnce_IsValid(t *testing.T) {
	doc := callDocument(callWithInputs("C", "Mod",
		callInput("Depts", `{"Rev":"Engineering"}`),
		callInput("Assignees", fmt.Sprintf(`{"M_T":%q}`, validCandidateUser)),
		callInput("clarificationId", "=id"),
		callInput("clarificationId", "=other")))

	if blocking := blockingErrors(validateDocument(t, doc)); len(blocking) > 0 {
		t.Errorf("a call with each Depts and Assignees input once must be valid: %+v", blocking)
	}
}
