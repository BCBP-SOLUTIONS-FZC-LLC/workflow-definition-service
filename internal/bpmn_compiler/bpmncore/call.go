package bpmncore

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"
)

// Call-site inputs the compiler consumes instead of passing to the called
// process: Depts binds the called process's open lanes, Assignees gives its
// tasks default users for this call, and dept_id is a retired form of Depts.
const (
	CallInputDepts     = "Depts"
	CallInputAssignees = "Assignees"
	CallInputDeptID    = "dept_id"
)

// CallInputs are a callActivity's compiler-consumed inputs, parsed.
type CallInputs struct {
	Depts        map[string]string
	DeptsErr     error
	Assignees    map[string]string
	AssigneesErr error
	HasDeptID    bool
}

func ParseCallInputs(ca BPMNCallActivity) CallInputs {
	var in CallInputs
	m := ca.ExtensionElements.IOMapping
	if m == nil {
		return in
	}
	for _, v := range m.Inputs {
		switch v.Target {
		case CallInputDepts:
			in.Depts, in.DeptsErr = parseStringObject(v.Source)
		case CallInputAssignees:
			in.Assignees, in.AssigneesErr = parseStringObject(v.Source)
		case CallInputDeptID:
			in.HasDeptID = true
		}
	}
	return in
}

func IsCallInput(target string) bool {
	return target == CallInputDepts || target == CallInputAssignees || target == CallInputDeptID
}

// parseStringObject reads a JSON object of strings, written plain or, as the
// Camunda properties panel writes an expression, with a leading "=".
func parseStringObject(source string) (map[string]string, error) {
	src := strings.TrimPrefix(strings.TrimSpace(source), "=")
	var out map[string]string
	if err := json.Unmarshal([]byte(src), &out); err != nil {
		return nil, fmt.Errorf("not a JSON object of strings: %w", err)
	}
	return out, nil
}

// LaneNamed finds a lane of proc by name, ignoring case.
func LaneNamed(proc *BPMNProcess, name string) *BPMNLane {
	for i := range proc.LaneSet.Lanes {
		if strings.EqualFold(proc.LaneSet.Lanes[i].Name, name) {
			return &proc.LaneSet.Lanes[i]
		}
	}
	return nil
}

func LaneDeptID(lane *BPMNLane) string {
	for _, p := range lane.ExtensionElements.ZeebeProps.Items {
		if p.Name == "dept_id" {
			return p.Value
		}
	}
	return ""
}

// WorkLanes returns the lanes of proc that hold work: a task, a callActivity
// or a subProcess. Only these become departments of its plan.
func WorkLanes(proc *BPMNProcess) []*BPMNLane {
	work := map[string]bool{}
	for _, t := range proc.UserTasks {
		work[t.ID] = true
	}
	for _, t := range proc.SendTasks {
		work[t.ID] = true
	}
	for _, t := range proc.ReceiveTasks {
		work[t.ID] = true
	}
	for _, ca := range proc.CallActivities {
		work[ca.ID] = true
	}
	for _, sp := range proc.SubProcesses {
		work[sp.ID] = true
	}
	var lanes []*BPMNLane
	for i := range proc.LaneSet.Lanes {
		lane := &proc.LaneSet.Lanes[i]
		if slices.ContainsFunc(lane.FlowNodeRefs, func(id string) bool { return work[id] }) {
			lanes = append(lanes, lane)
		}
	}
	return lanes
}

// OpenSlots names the departments of a called process that its caller binds:
// every work lane with no dept_id of its own, or, for a laneless process, its
// single implicit department, named by the process ID.
func OpenSlots(proc *BPMNProcess) []string {
	if len(proc.LaneSet.Lanes) == 0 {
		return []string{proc.ID}
	}
	var open []string
	for _, lane := range WorkLanes(proc) {
		if LaneDeptID(lane) == "" {
			open = append(open, lane.Name)
		}
	}
	return open
}

// CalledPlans collects the plans of called processes during one compile, so
// each is compiled once however many calls name it.
type CalledPlans struct {
	plans map[string]*dsl.CompiledPlan
}

func NewCalledPlans() *CalledPlans {
	return &CalledPlans{plans: map[string]*dsl.CompiledPlan{}}
}

// Claim reports whether id still needs compiling, and marks it claimed so a
// call reached again while it compiles does not compile it twice.
func (c *CalledPlans) Claim(id string) bool {
	if _, ok := c.plans[id]; ok {
		return false
	}
	c.plans[id] = nil
	return true
}

func (c *CalledPlans) Put(plan *dsl.CompiledPlan) { c.plans[plan.Name] = plan }

// All returns the compiled plans ordered by name.
func (c *CalledPlans) All() []*dsl.CompiledPlan {
	var out []*dsl.CompiledPlan
	for _, name := range slices.Sorted(maps.Keys(c.plans)) {
		if p := c.plans[name]; p != nil {
			out = append(out, p)
		}
	}
	return out
}
