package bpmn_compiler_test

import (
	"fmt"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func scheduledTaskXML(id, dueDate, followUpDate string) string {
	return fmt.Sprintf("    <bpmn:userTask id=%q name=%q><bpmn:extensionElements><zeebe:taskDefinition type=\"review\"/><zeebe:taskSchedule dueDate=%q followUpDate=%q/></bpmn:extensionElements></bpmn:userTask>\n", id, id, dueDate, followUpDate)
}

func scheduledDocument(t *testing.T, dueDate, followUpDate string) []domain.BPMNValidationError {
	t.Helper()
	body := "    <bpmn:startEvent id=\"S\"/>\n" + scheduledTaskXML("A", dueDate, followUpDate) + "    <bpmn:endEvent id=\"E\"/>\n" +
		flowXML("F1", "S", "A") + flowXML("F2", "A", "E")
	return validateDocument(t, flowDocument(oneLane("S", "A", "E"), body))
}

func TestValidate_TaskScheduleDatesThatAreNotRFC3339_AreRefused(t *testing.T) {
	for name, tc := range map[string][2]string{
		"duration":     {"P3D", ""},
		"plain text":   {"a", ""},
		"date only":    {"2026-10-12", ""},
		"follow-up":    {"", "tomorrow"},
		"both invalid": {"P3D", "P1D"},
	} {
		t.Run(name, func(t *testing.T) {
			assertErrorOn(t, scheduledDocument(t, tc[0], tc[1]), domain.BPMNErrInvalidTaskSchedule, "A")
		})
	}
}

func TestValidate_TaskScheduleDatesInRFC3339_AreAccepted(t *testing.T) {
	for _, tc := range [][2]string{{"", ""}, {"2026-10-12T09:00:00Z", ""}, {"2026-10-12T09:00:00+04:00", "2026-10-11T09:00:00Z"}} {
		assertNoError(t, scheduledDocument(t, tc[0], tc[1]), domain.BPMNErrInvalidTaskSchedule)
	}
}
