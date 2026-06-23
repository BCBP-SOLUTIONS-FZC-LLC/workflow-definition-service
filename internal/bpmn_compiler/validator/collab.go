package validator

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// ValidateCollaboration validates a <bpmn:collaboration> element.
func ValidateCollaboration(collab *bpmncore.BPMNCollaboration, defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	if collab == nil {
		return nil
	}

	msgIDs := make(map[string]struct{}, len(defs.Messages))
	for _, m := range defs.Messages {
		msgIDs[m.ID] = struct{}{}
	}

	knownRefs := collabReachableIDs(collab, defs)

	var errs []domain.BPMNValidationError
	for _, mf := range collab.MessageFlows {
		if mf.MessageRef != "" {
			if _, ok := msgIDs[mf.MessageRef]; !ok {
				errs = AppendErr(errs, domain.BPMNErrMissingMessageDefinition, mf.ID,
					fmt.Sprintf("messageFlow references unknown message %q; declare it as a <bpmn:message> element", mf.MessageRef))
			}
		}
		if _, ok := knownRefs[mf.SourceRef]; !ok {
			errs = AppendErr(errs, domain.BPMNErrUnmatchedMessageFlow, mf.ID,
				fmt.Sprintf("messageFlow sourceRef %q does not reference a known participant or flow node", mf.SourceRef))
		}
		if _, ok := knownRefs[mf.TargetRef]; !ok {
			errs = AppendErr(errs, domain.BPMNErrUnmatchedMessageFlow, mf.ID,
				fmt.Sprintf("messageFlow targetRef %q does not reference a known participant or flow node", mf.TargetRef))
		}
	}
	return errs
}

func collabReachableIDs(collab *bpmncore.BPMNCollaboration, defs *bpmncore.BPMNDefinitions) map[string]struct{} {
	ids := make(map[string]struct{})
	for _, p := range collab.Participants {
		ids[p.ID] = struct{}{}
	}
	for i := range defs.Processes {
		proc := &defs.Processes[i]
		for _, t := range proc.UserTasks {
			ids[t.ID] = struct{}{}
		}
		for _, t := range proc.SendTasks {
			ids[t.ID] = struct{}{}
		}
		for _, t := range proc.ReceiveTasks {
			ids[t.ID] = struct{}{}
		}
		for _, e := range proc.StartEvents {
			ids[e.ID] = struct{}{}
		}
		for _, e := range proc.EndEvents {
			ids[e.ID] = struct{}{}
		}
	}
	return ids
}
