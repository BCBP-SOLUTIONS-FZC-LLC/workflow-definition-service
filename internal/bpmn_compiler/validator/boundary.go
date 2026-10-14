package validator

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func ValidateMessageBoundaryNames(proc *bpmncore.BPMNProcess, defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	for _, be := range proc.BoundaryEvents {
		if be.Message == nil {
			continue
		}
		name := bpmncore.ResolveMessageName(be.Message.MessageRef, defs)
		if name == "" {
			name = bpmncore.ResolveMessageFlowTarget(be.ID, defs)
		}
		if name == "" {
			errs = AppendWarn(errs, domain.BPMNErrMissingMessageDefinition, be.ID,
				fmt.Sprintf("message boundary event has no resolvable message name; declare a <bpmn:message> or add a messageFlow targeting %q", be.ID))
		}
	}
	return errs
}

func ValidateBoundaryEvents(proc *bpmncore.BPMNProcess, g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	timerCountByHost := make(map[string]int)
	messageCountByHost := make(map[string]int)
	var errs []domain.BPMNValidationError
	for _, be := range proc.BoundaryEvents {
		if _, ok := g.NodeIDs[be.AttachedToRef]; !ok {
			errs = AppendErr(errs, domain.BPMNErrDanglingNode, be.ID,
				fmt.Sprintf("boundary event attachedToRef %q does not reference a known node", be.AttachedToRef))
			continue
		}
		if !bpmncore.ValidCancelActivity(be.CancelActivity) {
			errs = AppendErr(errs, domain.BPMNErrInvalidBoundaryAttachment, be.ID,
				fmt.Sprintf("boundary event cancelActivity %q must be true, false, 1 or 0", be.CancelActivity))
		}
		defErrs, skip := validateBoundaryEventDef(be)
		errs = append(errs, defErrs...)
		if skip {
			continue
		}
		errs = append(errs, validateBoundaryHost(be, g)...)
		if be.Timer != nil {
			errs = append(errs, validateTimerBoundary(be, timerCountByHost)...)
		}
		if be.Message != nil {
			errs = append(errs, validateMessageBoundaryCount(be, messageCountByHost)...)
		}
		if be.Error != nil {
			errs = append(errs, validateErrorBoundary(be, g)...)
		}
		if n := forwardOutCount(be.ID, g, backEdges); n > 1 {
			errs = AppendErr(errs, domain.BPMNErrSplitWithoutGateway, be.ID,
				fmt.Sprintf("boundary event has %d outgoing flows; its path continues along one flow, so branch with a gateway after its first task", n))
		}
		errs = append(errs, validateBoundaryTarget(be, proc, g, backEdges)...)
	}
	return errs
}

func validateBoundaryHost(be bpmncore.BPMNBoundaryEvent, g *bpmncore.Graph) []domain.BPMNValidationError {
	host := g.NodeType[be.AttachedToRef]
	switch {
	case be.Error != nil:
		return nil
	case host == bpmncore.NodeTypeUserTask, host == bpmncore.NodeTypeSubProcess, host == bpmncore.NodeTypeCallActivity:
		return nil
	}
	return AppendErr(nil, domain.BPMNErrInvalidBoundaryAttachment, be.ID,
		fmt.Sprintf("timer and message boundary events attach to a userTask, subProcess or callActivity, not to node %q", be.AttachedToRef))
}

func validateMessageBoundaryCount(be bpmncore.BPMNBoundaryEvent, countByHost map[string]int) []domain.BPMNValidationError {
	countByHost[be.AttachedToRef]++
	if countByHost[be.AttachedToRef] > 1 {
		return AppendErr(nil, domain.BPMNErrInvalidBoundaryAttachment, be.ID,
			fmt.Sprintf("node %q already has a message boundary event; only one is allowed", be.AttachedToRef))
	}
	return nil
}

func ValidateErrorBoundaryRefs(proc *bpmncore.BPMNProcess, defs *bpmncore.BPMNDefinitions) []domain.BPMNValidationError {
	known := make(map[string]struct{}, len(defs.Errors))
	for _, e := range defs.Errors {
		known[e.ID] = struct{}{}
	}
	var errs []domain.BPMNValidationError
	for _, be := range proc.BoundaryEvents {
		if be.Error == nil || be.Error.ErrorRef == "" {
			continue
		}
		if _, ok := known[be.Error.ErrorRef]; !ok {
			errs = AppendErr(errs, domain.BPMNErrInvalidBoundaryAttachment, be.ID,
				fmt.Sprintf("error boundary event errorRef %q does not reference a known bpmn:error element", be.Error.ErrorRef))
		}
	}
	return errs
}

func validateBoundaryTarget(be bpmncore.BPMNBoundaryEvent, proc *bpmncore.BPMNProcess, g *bpmncore.Graph, backEdges map[[2]string]bool) []domain.BPMNValidationError {
	targets := []bpmncore.FlowNodeType{bpmncore.NodeTypeUserTask, bpmncore.NodeTypeSubProcess, bpmncore.NodeTypeEndEvent}
	untargetable := []bpmncore.FlowNodeType{bpmncore.NodeTypeCallActivity, bpmncore.NodeTypeSendTask, bpmncore.NodeTypeReceiveTask}
	skipped := bpmncore.SkippedTasks(proc, g)
	visited := map[string]bool{}
	for curr := bpmncore.OutgoingTargetOf(be.ID, proc); curr != "" && !visited[curr]; {
		visited[curr] = true
		if skipped[curr] {
			next, ok := bpmncore.FirstForward(curr, g, backEdges)
			if !ok {
				return nil
			}
			curr = next
			continue
		}
		if g.NodeType[curr] == bpmncore.NodeTypeSubProcess && !bodyStartsWithTask(proc, curr) {
			return AppendErr(nil, domain.BPMNErrUnsupportedBoundaryTarget, be.ID,
				fmt.Sprintf("boundary event leads to subProcess %q, whose body does not start with a userTask; a boundary runs one department from its first task, so start the body with a userTask", curr))
		}
		if slices.Contains(targets, g.NodeType[curr]) {
			return nil
		}
		if slices.Contains(untargetable, g.NodeType[curr]) {
			return AppendErr(nil, domain.BPMNErrUnsupportedBoundaryTarget, be.ID,
				fmt.Sprintf("boundary event leads to %s %q; a boundary's path must start with a userTask or subProcess, or end, so put a userTask before it", g.NodeType[curr], curr))
		}
		next, ok := bpmncore.FirstForward(curr, g, backEdges)
		if !ok {
			return nil
		}
		curr = next
	}
	return nil
}

func validateBoundaryEventDef(be bpmncore.BPMNBoundaryEvent) (errs []domain.BPMNValidationError, skip bool) {
	hasTimer := be.Timer != nil
	hasError := be.Error != nil
	hasMessage := be.Message != nil
	count := 0
	if hasTimer {
		count++
	}
	if hasError {
		count++
	}
	if hasMessage {
		count++
	}
	if count > 1 {
		return AppendErr(nil, domain.BPMNErrInvalidBoundaryAttachment, be.ID,
			"boundary event must have exactly one event definition (timer, error, or message), not multiple"), true
	}
	if count == 0 {
		return AppendErr(nil, domain.BPMNErrMissingTaskDefinition, be.ID,
			"boundary event has no event definition; must be a timer, error, or message boundary event"), true
	}
	return nil, false
}

func validateTimerBoundary(be bpmncore.BPMNBoundaryEvent, timerCountByHost map[string]int) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	if !IsValidDuration(be.Timer.Duration) {
		errs = AppendErr(errs, domain.BPMNErrInvalidSLADuration, be.ID,
			fmt.Sprintf("timer boundary event has invalid duration %q; use a positive Go duration (72h) or ISO 8601 period (P3D)", be.Timer.Duration))
	}
	timerCountByHost[be.AttachedToRef]++
	if timerCountByHost[be.AttachedToRef] > 1 {
		errs = AppendErr(errs, domain.BPMNErrInvalidBoundaryAttachment, be.ID,
			fmt.Sprintf("node %q already has a timer boundary event; only one is allowed", be.AttachedToRef))
	}
	return errs
}

func validateErrorBoundary(be bpmncore.BPMNBoundaryEvent, g *bpmncore.Graph) []domain.BPMNValidationError {
	t := g.NodeType[be.AttachedToRef]
	if t != bpmncore.NodeTypeSubProcess && t != bpmncore.NodeTypeCallActivity {
		return AppendErr(nil, domain.BPMNErrInvalidBoundaryAttachment, be.ID,
			fmt.Sprintf("error boundary event can only be attached to a subProcess or callActivity, not to node %q", be.AttachedToRef))
	}
	return nil
}

var iso8601DurationRe = regexp.MustCompile(`^P(?:(\d+)W|(?:(\d+)D)?(?:T(?:(\d+)H)?(?:(\d+)M)?(?:(\d+)S)?)?)$`)

var iso8601Units = [...]time.Duration{7 * 24 * time.Hour, 24 * time.Hour, time.Hour, time.Minute, time.Second}

func IsValidDuration(s string) bool {
	if d, err := time.ParseDuration(s); err == nil {
		return d > 0
	}
	return iso8601Positive(s)
}

func iso8601Positive(s string) bool {
	m := iso8601DurationRe.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	var total time.Duration
	for i, unit := range iso8601Units {
		if m[i+1] == "" {
			continue
		}
		n, err := strconv.ParseInt(m[i+1], 10, 64)
		if err != nil || n > int64(math.MaxInt64/unit) || total > math.MaxInt64-time.Duration(n)*unit {
			return false
		}
		total += time.Duration(n) * unit
	}
	return total > 0
}

func bodyStartsWithTask(proc *bpmncore.BPMNProcess, spID string) bool {
	sp := bpmncore.FindSubProcess(proc, spID)
	if sp == nil || len(sp.StartEvents) == 0 {
		return true
	}
	body := bpmncore.BuildGraph(bpmncore.SubProcToProcess(sp))
	next := body.Outgoing[sp.StartEvents[0].ID]
	return len(next) > 0 && body.NodeType[next[0]] == bpmncore.NodeTypeUserTask
}
