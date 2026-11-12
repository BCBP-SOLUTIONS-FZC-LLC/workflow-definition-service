package bpmn_compiler

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/bpmncore"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/bpmn_compiler/validator"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func w4bFlows(pairs ...string) []bpmncore.BPMNSequenceFlow {
	flows := make([]bpmncore.BPMNSequenceFlow, 0, len(pairs))
	for i, p := range pairs {
		from, to, _ := strings.Cut(p, ">")
		flows = append(flows, bpmncore.BPMNSequenceFlow{ID: fmt.Sprintf("F%d", i+1), SourceRef: from, TargetRef: to})
	}
	return flows
}

func w4bSig(errs []domain.BPMNValidationError) []string {
	sig := make([]string, 0, len(errs))
	for _, e := range errs {
		sig = append(sig, string(e.Code)+"@"+e.NodeID)
	}
	slices.Sort(sig)
	return sig
}

func w4bSigWithDirection(errs []domain.BPMNValidationError) []string {
	sig := make([]string, 0, len(errs))
	for _, e := range errs {
		dir := ""
		switch {
		case strings.Contains(e.Message, "no incoming"):
			dir = ":incoming"
		case strings.Contains(e.Message, "no outgoing"):
			dir = ":outgoing"
		}
		sig = append(sig, string(e.Code)+"@"+e.NodeID+dir)
	}
	slices.Sort(sig)
	return sig
}

func w4bWant(items ...string) []string {
	out := slices.Clone(items)
	slices.Sort(out)
	return out
}

func w4bEvents(ids ...string) []bpmncore.BPMNEvent {
	out := make([]bpmncore.BPMNEvent, 0, len(ids))
	for _, id := range ids {
		out = append(out, bpmncore.BPMNEvent{ID: id})
	}
	return out
}

func w4bCompare(t *testing.T, got, want []string, errs []domain.BPMNValidationError) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v (full errors: %v)", got, want, errs)
	}
}

type w4bNodeKind struct {
	name string
	add  func(p *bpmncore.BPMNProcess, id string)
}

var w4bTaskLikeKinds = []w4bNodeKind{
	{"user task", func(p *bpmncore.BPMNProcess, id string) {
		p.UserTasks = append(p.UserTasks, bpmncore.BPMNUserTask{ID: id})
	}},
	{"send task", func(p *bpmncore.BPMNProcess, id string) {
		p.SendTasks = append(p.SendTasks, bpmncore.BPMNSendTask{ID: id})
	}},
	{"receive task", func(p *bpmncore.BPMNProcess, id string) {
		p.ReceiveTasks = append(p.ReceiveTasks, bpmncore.BPMNReceiveTask{ID: id})
	}},
	{"sub-process", func(p *bpmncore.BPMNProcess, id string) {
		p.SubProcesses = append(p.SubProcesses, bpmncore.BPMNSubProcess{ID: id})
	}},
	{"call activity", func(p *bpmncore.BPMNProcess, id string) {
		p.CallActivities = append(p.CallActivities, bpmncore.BPMNCallActivity{ID: id})
	}},
}

var w4bGatewayKinds = []w4bNodeKind{
	{"parallel gateway", func(p *bpmncore.BPMNProcess, id string) {
		p.ParallelGateways = append(p.ParallelGateways, bpmncore.BPMNGateway{ID: id})
	}},
	{"exclusive gateway", func(p *bpmncore.BPMNProcess, id string) {
		p.ExclusiveGateways = append(p.ExclusiveGateways, bpmncore.BPMNGateway{ID: id})
	}},
	{"inclusive gateway", func(p *bpmncore.BPMNProcess, id string) {
		p.InclusiveGateways = append(p.InclusiveGateways, bpmncore.BPMNGateway{ID: id})
	}},
}

func TestValidateBoundaryEvents_TwoForwardFlowsNeedAGateway_BackEdgesAreNotCounted(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		flows     []string
		backEdges map[[2]string]bool
		want      []string
		wantCount string
	}{
		{name: "two forward flows", flows: []string{"BE>T2", "BE>E1"}, want: w4bWant("SPLIT_WITHOUT_GATEWAY@BE"), wantCount: "2 outgoing flows"},
		{name: "one forward flow and one back-edge", flows: []string{"BE>T2", "BE>T"}, backEdges: map[[2]string]bool{{"BE", "T"}: true}, want: w4bWant()},
		{name: "two forward flows and one back-edge count two", flows: []string{"BE>T2", "BE>E1", "BE>T"}, backEdges: map[[2]string]bool{{"BE", "T"}: true}, want: w4bWant("SPLIT_WITHOUT_GATEWAY@BE"), wantCount: "2 outgoing flows"},
		{name: "a back-edge only", flows: []string{"BE>T"}, backEdges: map[[2]string]bool{{"BE", "T"}: true}, want: w4bWant()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proc := &bpmncore.BPMNProcess{
				UserTasks:      []bpmncore.BPMNUserTask{{ID: "T"}, {ID: "T2"}},
				EndEvents:      w4bEvents("E1"),
				BoundaryEvents: []bpmncore.BPMNBoundaryEvent{{ID: "BE", AttachedToRef: "T", Timer: &bpmncore.BPMNTimerDef{Duration: "1h"}}},
				SequenceFlows:  w4bFlows(tc.flows...),
			}
			errs := validator.ValidateBoundaryEvents(proc, bpmncore.BuildGraph(proc), tc.backEdges)
			w4bCompare(t, w4bSig(errs), tc.want, errs)
			if tc.wantCount != "" && !strings.Contains(errs[0].Message, tc.wantCount) {
				t.Errorf("message %q does not say %q", errs[0].Message, tc.wantCount)
			}
		})
	}
}

func TestValidateMessageBoundaryNames_WarnsOnlyWhenNoNameSourceResolves(t *testing.T) {
	t.Parallel()

	withFlow := func(mf bpmncore.BPMNMessageFlow, msgs ...bpmncore.BPMNMessage) *bpmncore.BPMNDefinitions {
		return &bpmncore.BPMNDefinitions{
			Messages:      msgs,
			Collaboration: &bpmncore.BPMNCollaboration{MessageFlows: []bpmncore.BPMNMessageFlow{mf}},
		}
	}
	cases := []struct {
		name     string
		boundary bpmncore.BPMNBoundaryEvent
		defs     *bpmncore.BPMNDefinitions
		wantWarn bool
	}{
		{
			name:     "no messageRef, no collaboration",
			boundary: bpmncore.BPMNBoundaryEvent{ID: "BE", AttachedToRef: "T", Message: &bpmncore.BPMNMessageEventDef{}},
			defs:     &bpmncore.BPMNDefinitions{},
			wantWarn: true,
		},
		{
			name:     "nil definitions",
			boundary: bpmncore.BPMNBoundaryEvent{ID: "BE", AttachedToRef: "T", Message: &bpmncore.BPMNMessageEventDef{}},
			defs:     nil,
			wantWarn: true,
		},
		{
			name:     "messageRef names a message that is not declared",
			boundary: bpmncore.BPMNBoundaryEvent{ID: "BE", AttachedToRef: "T", Message: &bpmncore.BPMNMessageEventDef{MessageRef: "Msg_missing"}},
			defs:     &bpmncore.BPMNDefinitions{Messages: []bpmncore.BPMNMessage{{ID: "Msg_other", Name: "other"}}},
			wantWarn: true,
		},
		{
			name:     "declared message has an empty name",
			boundary: bpmncore.BPMNBoundaryEvent{ID: "BE", AttachedToRef: "T", Message: &bpmncore.BPMNMessageEventDef{MessageRef: "Msg_1"}},
			defs:     &bpmncore.BPMNDefinitions{Messages: []bpmncore.BPMNMessage{{ID: "Msg_1"}}},
			wantWarn: true,
		},
		{
			name:     "a message flow targets another node",
			boundary: bpmncore.BPMNBoundaryEvent{ID: "BE", AttachedToRef: "T", Message: &bpmncore.BPMNMessageEventDef{}},
			defs:     withFlow(bpmncore.BPMNMessageFlow{ID: "MF", Name: "ping", SourceRef: "X", TargetRef: "OtherNode"}),
			wantWarn: true,
		},
		{
			name:     "the messageRef resolves to a name",
			boundary: bpmncore.BPMNBoundaryEvent{ID: "BE", AttachedToRef: "T", Message: &bpmncore.BPMNMessageEventDef{MessageRef: "Msg_1"}},
			defs:     &bpmncore.BPMNDefinitions{Messages: []bpmncore.BPMNMessage{{ID: "Msg_1", Name: "approved"}}},
		},
		{
			name:     "a named message flow targets the boundary",
			boundary: bpmncore.BPMNBoundaryEvent{ID: "BE", AttachedToRef: "T", Message: &bpmncore.BPMNMessageEventDef{}},
			defs:     withFlow(bpmncore.BPMNMessageFlow{ID: "MF", Name: "ping", SourceRef: "X", TargetRef: "BE"}),
		},
		{
			name:     "a timer boundary is not a message boundary",
			boundary: bpmncore.BPMNBoundaryEvent{ID: "BE", AttachedToRef: "T", Timer: &bpmncore.BPMNTimerDef{Duration: "1h"}},
			defs:     &bpmncore.BPMNDefinitions{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proc := &bpmncore.BPMNProcess{BoundaryEvents: []bpmncore.BPMNBoundaryEvent{tc.boundary}}
			errs := validator.ValidateMessageBoundaryNames(proc, tc.defs)
			if !tc.wantWarn {
				w4bCompare(t, w4bSig(errs), w4bWant(), errs)
				return
			}
			w4bCompare(t, w4bSig(errs), w4bWant("MISSING_MESSAGE_DEFINITION@BE"), errs)
			if errs[0].Severity != domain.SeverityWarning {
				t.Errorf("severity = %q, want a warning", errs[0].Severity)
			}
		})
	}
}

func TestIsValidDuration_ISO8601PeriodForms(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want bool
	}{
		{"P3D", true},
		{"PT1H30M", true},
		{"P2W", true},
		{"P1W", true},
		{"P1DT12H", true},
		{"P0D", false},
		{"PT0S", false},
		{"P0W", false},
		{"P", false},
		{"PT", false},
		{"P1W2D", false},
		{"P1WT1H", false},
		{"P1Y", false},
		{"P1M", false},
		{"p3d", false},
		{"pt1h", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := validator.IsValidDuration(tc.in); got != tc.want {
				t.Errorf("IsValidDuration(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func w4bCallProcess(id string, userTasks int, calls ...string) bpmncore.BPMNProcess {
	p := bpmncore.BPMNProcess{ID: id, UserTasks: make([]bpmncore.BPMNUserTask, userTasks)}
	for i, target := range calls {
		ca := bpmncore.BPMNCallActivity{ID: fmt.Sprintf("call_%s_%d", id, i)}
		if target != "" {
			ca.ExtensionElements.ZeebeCalledElement = &bpmncore.ZeebeCalledElement{ProcessID: target}
		}
		p.CallActivities = append(p.CallActivities, ca)
	}
	return p
}

func w4bIgnoredProcess(p bpmncore.BPMNProcess) bpmncore.BPMNProcess {
	p.ExtensionElements.ZeebeProps = bpmncore.BPMNZeebeProperties{Items: []bpmncore.ZeebeProperty{{Name: "ignore", Value: "true"}}}
	return p
}

func TestValidateCallGraph_CallsFromIgnoredOrToUnresolvedProcessesAreNotFollowed(t *testing.T) {
	t.Parallel()

	full := bpmncore.StageBudget
	cases := []struct {
		name  string
		procs []bpmncore.BPMNProcess
		want  []string
	}{
		{
			name:  "control: a call back to the caller closes a cycle",
			procs: []bpmncore.BPMNProcess{w4bCallProcess("A", 0, "B"), w4bCallProcess("B", 0, "A")},
			want:  w4bWant("CALLED_ELEMENT_CYCLE@call_B_0"),
		},
		{
			name:  "an ignored process's call is not followed, so no cycle",
			procs: []bpmncore.BPMNProcess{w4bIgnoredProcess(w4bCallProcess("A", 0, "B")), w4bCallProcess("B", 0, "A")},
			want:  w4bWant(),
		},
		{
			name:  "control: a caller with one task plus a full callee exceeds the task limit once expanded",
			procs: []bpmncore.BPMNProcess{w4bCallProcess("A", 1, "B"), w4bCallProcess("B", full)},
			want:  w4bWant("TASK_LIMIT_EXCEEDED@A"),
		},
		{
			name:  "an ignored process's call adds nothing to its expanded size",
			procs: []bpmncore.BPMNProcess{w4bIgnoredProcess(w4bCallProcess("A", 1, "B")), w4bCallProcess("B", full)},
			want:  w4bWant(),
		},
		{
			name:  "control: a full process with a resolved empty callee exceeds the limit once expanded",
			procs: []bpmncore.BPMNProcess{w4bCallProcess("A", full, "B"), w4bCallProcess("B", 0)},
			want:  w4bWant("TASK_LIMIT_EXCEEDED@A"),
		},
		{
			name:  "a call to an unknown process id is not counted: one task under the limit plus a resolved empty callee still fits",
			procs: []bpmncore.BPMNProcess{w4bCallProcess("A", full-1, "Missing", "B"), w4bCallProcess("B", 0)},
			want:  w4bWant(),
		},
		{
			name:  "a call with no called element is not counted: one task under the limit plus a resolved empty callee still fits",
			procs: []bpmncore.BPMNProcess{w4bCallProcess("A", full-1, "", "B"), w4bCallProcess("B", 0)},
			want:  w4bWant(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			errs := validator.ValidateCallGraph(&bpmncore.BPMNDefinitions{Processes: tc.procs})
			w4bCompare(t, w4bSig(errs), tc.want, errs)
		})
	}
}

func w4bConditionProc(expr string) (*bpmncore.BPMNProcess, *bpmncore.Graph) {
	proc := &bpmncore.BPMNProcess{
		ExclusiveGateways: []bpmncore.BPMNGateway{{ID: "GW"}},
		SequenceFlows:     w4bFlows("GW>T1", "GW>T2"),
	}
	proc.SequenceFlows[0].ConditionExpression = expr
	return proc, bpmncore.BuildGraph(proc)
}

func w4bConditionOfBytes(t *testing.T, literal string, total int) string {
	t.Helper()
	expr := `decision == "` + literal + `"`
	if len(expr) != total {
		t.Fatalf("test setup: expression is %d bytes, want %d", len(expr), total)
	}
	return expr
}

func TestValidateConditionExprs_LengthLimitIsMeasuredInBytes(t *testing.T) {
	t.Parallel()

	const prefixAndQuote = len(`decision == ""`)
	cases := []struct {
		name    string
		literal string
		total   int
		tooLong bool
	}{
		{"exactly 4096 ASCII bytes", strings.Repeat("a", 4096-prefixAndQuote), 4096, false},
		{"4097 ASCII bytes", strings.Repeat("a", 4097-prefixAndQuote), 4097, true},
		{"exactly 4096 bytes of two-byte characters", strings.Repeat("é", (4096-prefixAndQuote)/2), 4096, false},
		{"4098 bytes of two-byte characters is under 4096 characters but over the limit", strings.Repeat("é", (4098-prefixAndQuote)/2), 4098, true},
		{"4499 bytes of three-byte characters is far under 4096 characters but over the limit", strings.Repeat("日", (4499-prefixAndQuote)/3), 4499, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proc, g := w4bConditionProc(w4bConditionOfBytes(t, tc.literal, tc.total))
			errs := validator.ValidateConditionExprs(proc, g, nil)
			if !tc.tooLong {
				w4bCompare(t, w4bSig(errs), w4bWant(), errs)
				return
			}
			w4bCompare(t, w4bSig(errs), w4bWant("INVALID_CONDITION_EXPRESSION@F1"), errs)
			if !strings.Contains(errs[0].Message, "4096") {
				t.Errorf("message %q does not name the 4096 limit, so the length check did not produce it", errs[0].Message)
			}
		})
	}
}

func w4bBoundaryHostProc(boundaries []bpmncore.BPMNBoundaryEvent, flows ...string) (*bpmncore.BPMNProcess, *bpmncore.Graph) {
	proc := &bpmncore.BPMNProcess{
		UserTasks:      []bpmncore.BPMNUserTask{{ID: "T"}},
		SubProcesses:   []bpmncore.BPMNSubProcess{{ID: "SP"}},
		CallActivities: []bpmncore.BPMNCallActivity{{ID: "CA"}},
		SendTasks:      []bpmncore.BPMNSendTask{{ID: "SND"}},
		ReceiveTasks:   []bpmncore.BPMNReceiveTask{{ID: "RCV"}},
		BoundaryEvents: boundaries,
		SequenceFlows:  w4bFlows(flows...),
	}
	return proc, bpmncore.BuildGraph(proc)
}

func w4bTimer(id, host, duration string) bpmncore.BPMNBoundaryEvent {
	return bpmncore.BPMNBoundaryEvent{ID: id, AttachedToRef: host, Timer: &bpmncore.BPMNTimerDef{Duration: duration}}
}

func w4bMessage(id, host string) bpmncore.BPMNBoundaryEvent {
	return bpmncore.BPMNBoundaryEvent{ID: id, AttachedToRef: host, Message: &bpmncore.BPMNMessageEventDef{}}
}

func w4bErrorBoundary(id, host string) bpmncore.BPMNBoundaryEvent {
	return bpmncore.BPMNBoundaryEvent{ID: id, AttachedToRef: host, Error: &bpmncore.BPMNErrorDef{ErrorRef: "Err_1"}}
}

func w4bTimerAndError(id, host, duration string) bpmncore.BPMNBoundaryEvent {
	be := w4bTimer(id, host, duration)
	be.Error = &bpmncore.BPMNErrorDef{ErrorRef: "Err_1"}
	return be
}

func TestValidateBoundaryEvents_DefinitionErrorSkipsLaterChecks_EachKindRunsOnlyItsOwn(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		boundaries []bpmncore.BPMNBoundaryEvent
		flows      []string
		want       []string
	}{
		{
			name:       "timer and error together on a send task with a bad duration and two forward flows",
			boundaries: []bpmncore.BPMNBoundaryEvent{w4bTimerAndError("BE", "SND", "bogus")},
			flows:      []string{"BE>T", "BE>SP"},
			want:       w4bWant("INVALID_BOUNDARY_ATTACHMENT@BE"),
		},
		{
			name: "timer and message together on a send task",
			boundaries: []bpmncore.BPMNBoundaryEvent{func() bpmncore.BPMNBoundaryEvent {
				be := w4bTimer("BE", "SND", "1h")
				be.Message = &bpmncore.BPMNMessageEventDef{}
				return be
			}()},
			want: w4bWant("INVALID_BOUNDARY_ATTACHMENT@BE"),
		},
		{
			name:       "timer and error together whose path leads to a call activity",
			boundaries: []bpmncore.BPMNBoundaryEvent{w4bTimerAndError("BE", "T", "1h")},
			flows:      []string{"BE>CA"},
			want:       w4bWant("INVALID_BOUNDARY_ATTACHMENT@BE"),
		},
		{
			name:       "a boundary with a definition error does not count against its host",
			boundaries: []bpmncore.BPMNBoundaryEvent{w4bTimerAndError("BE1", "SP", "1h"), w4bTimer("BE2", "SP", "1h")},
			want:       w4bWant("INVALID_BOUNDARY_ATTACHMENT@BE1"),
		},
		{
			name:       "control: a path to a call activity is refused when the definition is valid",
			boundaries: []bpmncore.BPMNBoundaryEvent{w4bTimer("BE", "T", "1h")},
			flows:      []string{"BE>CA"},
			want:       w4bWant("UNSUPPORTED_BOUNDARY_TARGET@BE"),
		},
		{
			name:       "a valid timer on a user task",
			boundaries: []bpmncore.BPMNBoundaryEvent{w4bTimer("BE", "T", "1h")},
			want:       w4bWant(),
		},
		{
			name:       "valid timers on a sub-process and a call activity",
			boundaries: []bpmncore.BPMNBoundaryEvent{w4bTimer("BE1", "SP", "P1D"), w4bTimer("BE2", "CA", "2h")},
			want:       w4bWant(),
		},
		{
			name:       "a timer with a bad duration reports only the duration",
			boundaries: []bpmncore.BPMNBoundaryEvent{w4bTimer("BE", "T", "bogus")},
			want:       w4bWant("INVALID_SLA_DURATION@BE"),
		},
		{
			name:       "a timer on a send task reports the host and, independently, the duration",
			boundaries: []bpmncore.BPMNBoundaryEvent{w4bTimer("BE", "SND", "bogus")},
			want:       w4bWant("INVALID_BOUNDARY_ATTACHMENT@BE", "INVALID_SLA_DURATION@BE"),
		},
		{
			name:       "a valid message on a user task",
			boundaries: []bpmncore.BPMNBoundaryEvent{w4bMessage("BE", "T")},
			want:       w4bWant(),
		},
		{
			name:       "a second message on the same host is refused",
			boundaries: []bpmncore.BPMNBoundaryEvent{w4bMessage("BE1", "T"), w4bMessage("BE2", "T")},
			want:       w4bWant("INVALID_BOUNDARY_ATTACHMENT@BE2"),
		},
		{
			name:       "a message on a receive task reports the host only",
			boundaries: []bpmncore.BPMNBoundaryEvent{w4bMessage("BE", "RCV")},
			want:       w4bWant("INVALID_BOUNDARY_ATTACHMENT@BE"),
		},
		{
			name:       "a timer and a message on the same host do not count against each other",
			boundaries: []bpmncore.BPMNBoundaryEvent{w4bTimer("BE1", "T", "1h"), w4bMessage("BE2", "T")},
			want:       w4bWant(),
		},
		{
			name:       "a valid error on a sub-process",
			boundaries: []bpmncore.BPMNBoundaryEvent{w4bErrorBoundary("BE", "SP")},
			want:       w4bWant(),
		},
		{
			name:       "an error on a user task reports one attachment error",
			boundaries: []bpmncore.BPMNBoundaryEvent{w4bErrorBoundary("BE", "T")},
			want:       w4bWant("INVALID_BOUNDARY_ATTACHMENT@BE"),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proc, g := w4bBoundaryHostProc(tc.boundaries, tc.flows...)
			errs := validator.ValidateBoundaryEvents(proc, g, nil)
			w4bCompare(t, w4bSig(errs), tc.want, errs)
		})
	}
}

func TestValidateBoundaryEvents_NoDefinition_SkipsHostPerKindAndTargetChecks(t *testing.T) {
	t.Parallel()

	proc, g := w4bBoundaryHostProc([]bpmncore.BPMNBoundaryEvent{{ID: "BE", AttachedToRef: "SND"}}, "BE>CA", "BE>SP")
	errs := validator.ValidateBoundaryEvents(proc, g, nil)
	if len(errs) != 1 || errs[0].NodeID != "BE" {
		t.Fatalf("got %v, want exactly one error on BE", errs)
	}
	if errs[0].Code != domain.BPMNErrInvalidBoundaryAttachment {
		t.Errorf("a definition-less boundary reported %s, want INVALID_BOUNDARY_ATTACHMENT", errs[0].Code)
	}
	for _, skipped := range []domain.BPMNErrorCode{
		domain.BPMNErrSplitWithoutGateway,
		domain.BPMNErrUnsupportedBoundaryTarget,
	} {
		if errs[0].Code == skipped {
			t.Errorf("a definition-less boundary reported %s, a check that must be skipped", skipped)
		}
	}
}

func TestValidateLoops_BackEdgeFromAGatewayThatIsNotExclusive_IsUnguarded(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		add  func(p *bpmncore.BPMNProcess)
		want []string
	}{
		{"parallel gateway", func(p *bpmncore.BPMNProcess) { p.ParallelGateways = []bpmncore.BPMNGateway{{ID: "GW"}} }, w4bWant("UNGUARDED_LOOP@GW")},
		{"inclusive gateway", func(p *bpmncore.BPMNProcess) { p.InclusiveGateways = []bpmncore.BPMNGateway{{ID: "GW"}} }, w4bWant("UNGUARDED_LOOP@GW")},
		{"control: exclusive gateway", func(p *bpmncore.BPMNProcess) { p.ExclusiveGateways = []bpmncore.BPMNGateway{{ID: "GW"}} }, w4bWant()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proc := &bpmncore.BPMNProcess{
				StartEvents:   w4bEvents("S"),
				EndEvents:     w4bEvents("E"),
				UserTasks:     []bpmncore.BPMNUserTask{{ID: "T1"}, {ID: "T2"}},
				SequenceFlows: w4bFlows("S>T1", "T1>GW", "GW>T2", "T2>E", "GW>T1"),
			}
			tc.add(proc)
			g := bpmncore.BuildGraph(proc)
			backEdges := bpmncore.ProcessBackEdges(proc, g, "S")
			if len(backEdges) != 1 || !backEdges[[2]string{"GW", "T1"}] {
				t.Fatalf("test setup: back-edges = %v, want only GW->T1", backEdges)
			}
			errs := validator.ValidateLoops(g, backEdges)
			w4bCompare(t, w4bSig(errs), tc.want, errs)
			if len(errs) == 1 && !strings.Contains(errs[0].Message, "exclusive gateway") {
				t.Errorf("message %q does not say the back-edge must start at an exclusive gateway", errs[0].Message)
			}
		})
	}
}

func TestValidateCounts_StartEvents_ImplicitStartExemptsNoneOnly_AndEveryExtraStartIsReported(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		starts   []string
		implicit string
		want     []string
	}{
		{"no start and no implicit start", nil, "", w4bWant("NO_START_EVENT@")},
		{"no start with an implicit start", nil, "T1", w4bWant()},
		{"one start", []string{"S1"}, "", w4bWant()},
		{"two starts: the first is spared", []string{"S1", "S2"}, "", w4bWant("MULTIPLE_START_EVENTS@S2")},
		{"four starts: one error per extra start", []string{"S1", "S2", "S3", "S4"}, "", w4bWant("MULTIPLE_START_EVENTS@S2", "MULTIPLE_START_EVENTS@S3", "MULTIPLE_START_EVENTS@S4")},
		{"an implicit start does not excuse an extra start", []string{"S1", "S2"}, "T1", w4bWant("MULTIPLE_START_EVENTS@S2")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proc := &bpmncore.BPMNProcess{StartEvents: w4bEvents(tc.starts...), EndEvents: w4bEvents("E1")}
			errs := validator.ValidateCounts(proc, tc.implicit)
			w4bCompare(t, w4bSig(errs), tc.want, errs)
		})
	}
}

func TestValidateCounts_EndEvents_ImplicitStartWaivesTheCheck(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		starts   []string
		ends     []string
		implicit string
		want     []string
	}{
		{"no end event and no implicit start", []string{"S1"}, nil, "", w4bWant("NO_END_EVENT@")},
		{"no end event with an implicit start", nil, nil, "T1", w4bWant()},
		{"one end event", []string{"S1"}, []string{"E1"}, "", w4bWant()},
		{"several end events", []string{"S1"}, []string{"E1", "E2"}, "", w4bWant()},
		{"neither start nor end and no implicit start", nil, nil, "", w4bWant("NO_START_EVENT@", "NO_END_EVENT@")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proc := &bpmncore.BPMNProcess{StartEvents: w4bEvents(tc.starts...), EndEvents: w4bEvents(tc.ends...)}
			errs := validator.ValidateCounts(proc, tc.implicit)
			w4bCompare(t, w4bSig(errs), tc.want, errs)
		})
	}
}

func TestValidateCounts_LimitsAreInclusive(t *testing.T) {
	t.Parallel()

	laneSet := func(n int) bpmncore.BPMNLaneSet {
		lanes := make([]bpmncore.BPMNLane, n)
		for i := range lanes {
			lanes[i] = bpmncore.BPMNLane{ID: fmt.Sprintf("L%d", i), Name: fmt.Sprintf("lane%d", i)}
		}
		return bpmncore.BPMNLaneSet{Lanes: lanes}
	}
	cases := []struct {
		name  string
		tasks int
		lanes int
		want  []string
	}{
		{"user tasks at the stage budget", bpmncore.StageBudget, 0, w4bWant()},
		{"user tasks one over the stage budget", bpmncore.StageBudget + 1, 0, w4bWant("TASK_LIMIT_EXCEEDED@")},
		{"100 lanes", 0, 100, w4bWant()},
		{"101 lanes", 0, 101, w4bWant("LANE_LIMIT_EXCEEDED@")},
		{"both limits exceeded", bpmncore.StageBudget + 1, 101, w4bWant("TASK_LIMIT_EXCEEDED@", "LANE_LIMIT_EXCEEDED@")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proc := &bpmncore.BPMNProcess{
				StartEvents: w4bEvents("S1"),
				EndEvents:   w4bEvents("E1"),
				UserTasks:   make([]bpmncore.BPMNUserTask, tc.tasks),
				LaneSet:     laneSet(tc.lanes),
			}
			errs := validator.ValidateCounts(proc, "")
			w4bCompare(t, w4bSig(errs), tc.want, errs)
		})
	}
}

func TestValidateLaneNames_ReservedSeparatorsInANameAreRefused(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		laneName string
		want     []string
	}{
		{"slash inside", "Eng/Ops", w4bWant("INVALID_LANE_NAME@L1")},
		{"slash alone", "/", w4bWant("INVALID_LANE_NAME@L1")},
		{"call-scope separator inside", "Eng::Ops", w4bWant("INVALID_LANE_NAME@L1")},
		{"segment separator inside", "Eng~Ops", w4bWant("INVALID_LANE_NAME@L1")},
		{"segment separator at the end", "Eng~", w4bWant("INVALID_LANE_NAME@L1")},
		{"a single colon is allowed", "Eng:Ops", w4bWant()},
		{"a backslash is allowed", `Eng\Ops`, w4bWant()},
		{"a hyphen and space are allowed", "Eng - Ops", w4bWant()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proc := &bpmncore.BPMNProcess{LaneSet: bpmncore.BPMNLaneSet{Lanes: []bpmncore.BPMNLane{
				{ID: "L1", Name: tc.laneName, FlowNodeRefs: []string{"T1"}},
				{ID: "L2", Name: "Fine", FlowNodeRefs: []string{"T2"}},
			}}}
			errs := validator.ValidateLaneNames(proc)
			w4bCompare(t, w4bSig(errs), tc.want, errs)
		})
	}
}

func TestValidateSeqFlowRefs_OneErrorPerUnknownEnd_AtTheFlowID(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		source       string
		target       string
		want         []string
		wantMessages []string
	}{
		{"both ends known", "A", "B", w4bWant(), nil},
		{"unknown source", "ghost", "B", w4bWant("INVALID_SEQUENCE_FLOW_REF@F1"), []string{"sourceRef"}},
		{"unknown target", "A", "ghost", w4bWant("INVALID_SEQUENCE_FLOW_REF@F1"), []string{"targetRef"}},
		{"both unknown", "ghost1", "ghost2", w4bWant("INVALID_SEQUENCE_FLOW_REF@F1", "INVALID_SEQUENCE_FLOW_REF@F1"), []string{"sourceRef", "targetRef"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proc := &bpmncore.BPMNProcess{
				StartEvents:   w4bEvents("A"),
				EndEvents:     w4bEvents("B"),
				SequenceFlows: []bpmncore.BPMNSequenceFlow{{ID: "F1", SourceRef: tc.source, TargetRef: tc.target}},
			}
			errs := validator.ValidateSeqFlowRefs(proc, bpmncore.BuildGraph(proc))
			w4bCompare(t, w4bSig(errs), tc.want, errs)
			for i, part := range tc.wantMessages {
				if !strings.Contains(errs[i].Message, part) {
					t.Errorf("error %d message %q does not mention %s", i, errs[i].Message, part)
				}
			}
		})
	}
}

func TestValidateDanglingNodes_EveryWorkAndGatewayKindNeedsBothFlows(t *testing.T) {
	t.Parallel()

	variants := []struct {
		name  string
		start bool
		end   bool
		want  []string
	}{
		{"no flows", false, false, []string{":incoming", ":outgoing"}},
		{"incoming flow only", true, false, []string{":outgoing"}},
		{"outgoing flow only", false, true, []string{":incoming"}},
		{"both flows", true, true, nil},
	}
	for _, kind := range slices.Concat(w4bTaskLikeKinds, w4bGatewayKinds) {
		for _, v := range variants {
			t.Run(kind.name+"/"+v.name, func(t *testing.T) {
				t.Parallel()
				proc := &bpmncore.BPMNProcess{}
				kind.add(proc, "X")
				var flows []string
				if v.start {
					proc.StartEvents = w4bEvents("S")
					flows = append(flows, "S>X")
				}
				if v.end {
					proc.EndEvents = w4bEvents("E")
					flows = append(flows, "X>E")
				}
				proc.SequenceFlows = w4bFlows(flows...)
				errs := validator.ValidateDanglingNodes(proc, bpmncore.BuildGraph(proc), "")
				var want []string
				for _, dir := range v.want {
					want = append(want, "DANGLING_NODE@X"+dir)
				}
				w4bCompare(t, w4bSigWithDirection(errs), w4bWant(want...), errs)
			})
		}
	}
}

func TestValidateDanglingNodes_ImplicitStartIsExemptFromTheIncomingCheckOnly(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		implicit string
		start    bool
		end      bool
		want     []string
	}{
		{"implicit start with no flows still needs an outgoing flow", "X", false, false, []string{"DANGLING_NODE@X:outgoing"}},
		{"implicit start with an outgoing flow only", "X", false, true, nil},
		{"implicit start that also has an incoming flow", "X", true, true, nil},
		{"implicit start with an incoming flow only still needs an outgoing flow", "X", true, false, []string{"DANGLING_NODE@X:outgoing"}},
		{"another node as the implicit start gives X no exemption", "Y", false, true, []string{"DANGLING_NODE@X:incoming"}},
	}
	for _, kind := range w4bTaskLikeKinds {
		for _, tc := range cases {
			t.Run(kind.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				proc := &bpmncore.BPMNProcess{}
				kind.add(proc, "X")
				var flows []string
				if tc.start {
					proc.StartEvents = w4bEvents("S")
					flows = append(flows, "S>X")
				}
				if tc.end {
					proc.EndEvents = w4bEvents("E")
					flows = append(flows, "X>E")
				}
				proc.SequenceFlows = w4bFlows(flows...)
				errs := validator.ValidateDanglingNodes(proc, bpmncore.BuildGraph(proc), tc.implicit)
				w4bCompare(t, w4bSigWithDirection(errs), w4bWant(tc.want...), errs)
			})
		}
	}
}

func TestValidateDanglingNodes_BoundaryWithoutOutgoingFlow_MessageBoundaryIsExempt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		boundary bpmncore.BPMNBoundaryEvent
		withFlow bool
		want     []string
	}{
		{"timer boundary without an outgoing flow", w4bTimer("B", "T", "1h"), false, w4bWant("DANGLING_NODE@B")},
		{"error boundary without an outgoing flow", w4bErrorBoundary("B", "SP"), false, w4bWant("DANGLING_NODE@B")},
		{"message boundary without an outgoing flow is exempt", w4bMessage("B", "T"), false, w4bWant()},
		{"timer boundary with an outgoing flow", w4bTimer("B", "T", "1h"), true, w4bWant()},
		{"error boundary with an outgoing flow", w4bErrorBoundary("B", "SP"), true, w4bWant()},
		{"message boundary with an outgoing flow", w4bMessage("B", "T"), true, w4bWant()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proc := &bpmncore.BPMNProcess{BoundaryEvents: []bpmncore.BPMNBoundaryEvent{tc.boundary}}
			if tc.withFlow {
				proc.EndEvents = w4bEvents("E")
				proc.SequenceFlows = w4bFlows("B>E")
			}
			errs := validator.ValidateDanglingNodes(proc, bpmncore.BuildGraph(proc), "")
			w4bCompare(t, w4bSig(errs), tc.want, errs)
		})
	}
}

func TestValidateSingleOutgoing_EveryFlowKindMustBranchAtAGateway_BackEdgesAreNotCounted(t *testing.T) {
	t.Parallel()

	kinds := slices.Concat([]w4bNodeKind{
		{"start event", func(p *bpmncore.BPMNProcess, id string) {
			p.StartEvents = append(p.StartEvents, bpmncore.BPMNEvent{ID: id})
		}},
	}, w4bTaskLikeKinds)
	cases := []struct {
		name      string
		flows     []string
		backEdges map[[2]string]bool
		wantSplit bool
		wantCount string
	}{
		{"one outgoing flow", []string{"X>E1"}, nil, false, ""},
		{"two forward flows", []string{"X>E1", "X>E2"}, nil, true, "has 2 outgoing flows"},
		{"one forward flow and one back-edge", []string{"X>E1", "X>E2"}, map[[2]string]bool{{"X", "E2"}: true}, false, ""},
		{"two forward flows and one back-edge count two", []string{"X>E1", "X>E2", "X>E3"}, map[[2]string]bool{{"X", "E3"}: true}, true, "has 2 outgoing flows"},
	}
	for _, kind := range kinds {
		for _, tc := range cases {
			t.Run(kind.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				proc := &bpmncore.BPMNProcess{EndEvents: w4bEvents("E1", "E2", "E3"), SequenceFlows: w4bFlows(tc.flows...)}
				kind.add(proc, "X")
				errs := validator.ValidateSingleOutgoing(proc, bpmncore.BuildGraph(proc), tc.backEdges)
				if !tc.wantSplit {
					w4bCompare(t, w4bSig(errs), w4bWant(), errs)
					return
				}
				w4bCompare(t, w4bSig(errs), w4bWant("SPLIT_WITHOUT_GATEWAY@X"), errs)
				if !strings.Contains(errs[0].Message, tc.wantCount) {
					t.Errorf("message %q does not say %q", errs[0].Message, tc.wantCount)
				}
			})
		}
	}
}

func TestValidateSingleOutgoing_EndEventWithAForwardFlow_IsRefusedButABackEdgeIsNot(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		flows     []string
		backEdges map[[2]string]bool
		want      []string
	}{
		{"end event with a forward flow", []string{"EX>E1"}, nil, w4bWant("END_EVENT_WITH_OUTGOING_FLOW@EX")},
		{"end event with two forward flows is one error", []string{"EX>E1", "EX>E2"}, nil, w4bWant("END_EVENT_WITH_OUTGOING_FLOW@EX")},
		{"end event whose only outgoing flow is a back-edge", []string{"EX>E1"}, map[[2]string]bool{{"EX", "E1"}: true}, w4bWant()},
		{"end event with no outgoing flow", nil, nil, w4bWant()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			proc := &bpmncore.BPMNProcess{EndEvents: w4bEvents("EX", "E1", "E2"), SequenceFlows: w4bFlows(tc.flows...)}
			errs := validator.ValidateSingleOutgoing(proc, bpmncore.BuildGraph(proc), tc.backEdges)
			w4bCompare(t, w4bSig(errs), tc.want, errs)
		})
	}
}
