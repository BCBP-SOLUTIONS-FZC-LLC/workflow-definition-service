package validator

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// ValidateCollaboration checks a collaboration's message flows: each names a
// declared message and known nodes, and each flow into a receive task or
// message boundary that runs, in root or a process it calls, has a name it
// can wait for. A flow into anything else needs none: nothing waits for it.
func ValidateCollaboration(collab *bpmncore.BPMNCollaboration, defs *bpmncore.BPMNDefinitions, root *bpmncore.BPMNProcess) []domain.BPMNValidationError {
	if collab == nil {
		return nil
	}

	msgIDs := make(map[string]struct{}, len(defs.Messages))
	for _, m := range defs.Messages {
		msgIDs[m.ID] = struct{}{}
	}

	knownRefs := collabReachableIDs(collab, defs)
	waiting := map[string]bool{}
	for _, p := range bpmncore.RunningProcesses(defs, root) {
		skipped := bpmncore.SkippedTasks(p, bpmncore.BuildGraph(p))
		for _, t := range p.ReceiveTasks {
			waiting[t.ID] = !skipped[t.ID]
		}
		for _, be := range p.BoundaryEvents {
			waiting[be.ID] = be.Message != nil
		}
	}

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
		if waiting[mf.TargetRef] && bpmncore.ResolveMessageFlowName(&mf, defs) == "" {
			errs = AppendWarn(errs, domain.BPMNErrMissingMessageDefinition, mf.ID,
				"message flow has no resolvable name; the Execution Service cannot correlate this message — declare a <bpmn:message> and reference it via messageRef on the flow or the connected send/receive task/boundary event")
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
		for _, sp := range proc.SubProcesses {
			ids[sp.ID] = struct{}{}
		}
		for _, ca := range proc.CallActivities {
			ids[ca.ID] = struct{}{}
		}
		for _, be := range proc.BoundaryEvents {
			ids[be.ID] = struct{}{}
		}
	}
	return ids
}
