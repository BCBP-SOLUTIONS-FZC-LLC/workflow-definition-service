package validator

import (
	"fmt"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func ValidateTaskSchedules(proc *bpmncore.BPMNProcess) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	check := func(tasks []bpmncore.BPMNUserTask) {
		for _, t := range tasks {
			ts := t.ExtensionElements.TaskSchedule
			if ts == nil {
				continue
			}
			for _, date := range []struct{ attr, value string }{{"dueDate", ts.DueDate}, {"followUpDate", ts.FollowUpDate}} {
				if date.value == "" {
					continue
				}
				if _, err := time.Parse(time.RFC3339, date.value); err != nil {
					errs = AppendErr(errs, domain.BPMNErrInvalidTaskSchedule, t.ID,
						fmt.Sprintf("%s %q is not an RFC 3339 date-time such as 2026-10-12T09:00:00Z; the Execution Service ignores any other value", date.attr, date.value))
				}
			}
		}
	}
	check(proc.UserTasks)
	check(proc.GenericTasks)
	check(proc.ServiceTasks)
	return errs
}
