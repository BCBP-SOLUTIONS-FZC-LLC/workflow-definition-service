package validator

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

// ValidateBoundaryEvents validates all boundary events on a process.
// Called from the process-level Validate() rather than per-element dispatch because
// boundary events are aggregated (one timer per host check requires seeing all at once).
func ValidateBoundaryEvents(proc *bpmncore.BPMNProcess, g *bpmncore.Graph) []domain.BPMNValidationError {
	timerCountByHost := make(map[string]int)
	var errs []domain.BPMNValidationError
	for _, be := range proc.BoundaryEvents {
		if _, ok := g.NodeIDs[be.AttachedToRef]; !ok {
			errs = AppendErr(errs, domain.BPMNErrDanglingNode, be.ID,
				fmt.Sprintf("boundary event attachedToRef %q does not reference a known node", be.AttachedToRef))
			continue
		}
		defErrs, skip := validateBoundaryEventDef(be)
		errs = append(errs, defErrs...)
		if skip {
			continue
		}
		if be.Timer != nil {
			errs = append(errs, validateTimerBoundary(be, timerCountByHost)...)
		}
		if be.Error != nil {
			errs = append(errs, validateErrorBoundary(be, g)...)
		}
	}
	return errs
}

func validateBoundaryEventDef(be bpmncore.BPMNBoundaryEvent) (errs []domain.BPMNValidationError, skip bool) {
	hasTimer := be.Timer != nil
	hasError := be.Error != nil
	if hasTimer && hasError {
		return AppendErr(nil, domain.BPMNErrInvalidBoundaryAttachment, be.ID,
			"boundary event must have exactly one event definition (timer or error), not both"), true
	}
	if !hasTimer && !hasError {
		return AppendErr(nil, domain.BPMNErrMissingTaskDefinition, be.ID,
			"boundary event has no event definition; must be a timer or error boundary event"), true
	}
	return nil, false
}

func validateTimerBoundary(be bpmncore.BPMNBoundaryEvent, timerCountByHost map[string]int) []domain.BPMNValidationError {
	var errs []domain.BPMNValidationError
	if !IsValidDuration(be.Timer.Duration) {
		errs = AppendErr(errs, domain.BPMNErrInvalidSLADuration, be.ID,
			fmt.Sprintf("timer boundary event has invalid duration %q; use Go duration (72h) or ISO 8601 period (P3D)", be.Timer.Duration))
	}
	timerCountByHost[be.AttachedToRef]++
	if timerCountByHost[be.AttachedToRef] > 1 {
		errs = AppendErr(errs, domain.BPMNErrInvalidBoundaryAttachment, be.ID,
			fmt.Sprintf("node %q already has a timer boundary event; only one is allowed", be.AttachedToRef))
	}
	return errs
}

func validateErrorBoundary(be bpmncore.BPMNBoundaryEvent, g *bpmncore.Graph) []domain.BPMNValidationError {
	if g.NodeType[be.AttachedToRef] != bpmncore.NodeTypeSubProcess {
		return AppendErr(nil, domain.BPMNErrInvalidBoundaryAttachment, be.ID,
			fmt.Sprintf("error boundary event can only be attached to a subProcess, not to node %q", be.AttachedToRef))
	}
	return nil
}

// iso8601DurationRe matches ISO 8601 periods with day/time components only.
var iso8601DurationRe = regexp.MustCompile(`^P(\d+D)?(T(\d+H)?(\d+M)?(\d+S)?)?$`)

// IsValidDuration accepts Go duration syntax or ISO 8601 period with day/time components.
func IsValidDuration(s string) bool {
	if s == "" {
		return false
	}
	if _, err := time.ParseDuration(s); err == nil {
		return true
	}
	return iso8601DurationRe.MatchString(s) && strings.ContainsAny(s, "0123456789")
}
