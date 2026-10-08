package bpmncore

import (
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl"
)

const defaultTaskQueue = "wf-queue-default"

// SegmentSeparator numbers a lane's later segments: <lane>~2, <lane>~3.
const SegmentSeparator = "~"

// StageBudget is the task limit (definition LLD §3.1.4 code 9). A compile
// that has made more stages than this stops, since a document whose
// branches merge only in part can compile its merged tail once per branch.
const StageBudget = 1000

// nodeBudget bounds the nodes one compile visits, for the same reason: a
// region compiled once per branch need hold no task to grow.
const nodeBudget = 10 * StageBudget

// ErrStageBudget is the error a compile over StageBudget or nodeBudget
// returns.
var ErrStageBudget = errors.New("bpmncore: the compiled plan exceeds the stage budget")

// pendingTarget marks a department reference to the segment of a node that
// may not be compiled yet; ResolveTargets replaces it once every node is.
const pendingTarget = "\x00"

type sharedDeptsStore struct {
	depts       []dsl.DepartmentDef
	deptIdx     map[string]int
	segments    map[string]int
	nodeSegment map[string]string
	visited     map[string]bool
	// boundarySteps holds, by boundary event ID, the steps of the path each
	// boundary leads to; ResolveTargets puts them on the boundaries.
	boundarySteps map[string][]dsl.ExecutionStep
	stages        int
	nodes         int
}

type CompileState struct {
	Proc       *BPMNProcess
	G          *Graph
	laneLabels map[string]string
	condExprs  map[[2]string]string
	backEdges  map[[2]string]bool
	StageTypes map[string]StageTypeHandler
	Elements   map[FlowNodeType]ElementHandler
	Defs       *BPMNDefinitions
	// skipped holds the tasks of ignored lanes, which compile to nothing.
	skipped map[string]bool
	// StopAt, when non-empty, causes TraverseNode to return immediately without
	// processing the node. Used by branch states to stop at the gateway's join.
	StopAt string
	// Called collects the plans of called processes, shared by every state of
	// one compile.
	Called *CalledPlans

	sd      *sharedDeptsStore
	steps   []dsl.ExecutionStep
	seqBuf  []string
	visited map[string]bool

	// defaultLane and defaultIAMDeptID place a node that is in no lane: a
	// laneless process's nodes, or a subprocess body's.
	defaultLane, defaultIAMDeptID string
	openLane, openSegment         string
	// segmentStarts are the nodes a boundary moves to. Each starts a segment,
	// since a boundary runs its target's department from the top.
	segmentStarts map[string]bool
	// pathSegment is the segment each node compiled on this state's path, its
	// branch states' parents included, went into: a revert's target is such
	// a node, compiled in this branch's own copy.
	pathSegment map[string]string
	// stopAtCompiled makes a boundary path end where it meets a node another
	// state has compiled, so a path that rejoins the main flow does not
	// compile the main flow's tasks again.
	stopAtCompiled bool
	// rejoined is set when the boundary path being compiled, branches of it
	// included, meets a node the main flow compiled, so that the path
	// continues into it.
	rejoined *bool
}

func NewCompileState(
	proc *BPMNProcess,
	g *Graph,
	laneLabels map[string]string,
	condExprs map[[2]string]string,
	backEdges map[[2]string]bool,
	stageTypes map[string]StageTypeHandler,
	elements map[FlowNodeType]ElementHandler,
	defs *BPMNDefinitions,
) *CompileState {
	s := &CompileState{
		Proc:          proc,
		G:             g,
		laneLabels:    laneLabels,
		condExprs:     condExprs,
		backEdges:     backEdges,
		StageTypes:    stageTypes,
		Elements:      elements,
		Defs:          defs,
		Called:        NewCalledPlans(),
		sd:            &sharedDeptsStore{deptIdx: map[string]int{}, segments: map[string]int{}, nodeSegment: map[string]string{}, visited: map[string]bool{}, boundarySteps: map[string][]dsl.ExecutionStep{}},
		visited:       make(map[string]bool),
		segmentStarts: map[string]bool{},
		pathSegment:   map[string]string{},
		skipped:       SkippedTasks(proc, g),
	}
	if len(proc.LaneSet.Lanes) == 0 {
		s.defaultLane = ImplicitSlot(proc)
	}
	for _, be := range proc.BoundaryEvents {
		if node := s.taskAhead(OutgoingTargetOf(be.ID, proc)); node != "" {
			s.segmentStarts[node] = true
		}
	}
	return s
}

func NewBranchState(parent *CompileState, stopAt string) *CompileState {
	return &CompileState{
		Proc:             parent.Proc,
		G:                parent.G,
		laneLabels:       parent.laneLabels,
		condExprs:        parent.condExprs,
		backEdges:        parent.backEdges,
		StageTypes:       parent.StageTypes,
		Elements:         parent.Elements,
		Defs:             parent.Defs,
		StopAt:           stopAt,
		stopAtCompiled:   parent.stopAtCompiled,
		rejoined:         parent.rejoined,
		Called:           parent.Called,
		sd:               parent.sd,
		visited:          make(map[string]bool),
		defaultLane:      parent.defaultLane,
		defaultIAMDeptID: parent.defaultIAMDeptID,
		segmentStarts:    parent.segmentStarts,
		pathSegment:      maps.Clone(parent.pathSegment),
		skipped:          parent.skipped,
	}
}

// ShareSegmentsWith makes s, a subprocess body's state, fill the departments
// of parent, the state compiling the subprocess, so the body's tasks take
// their own segments of the parent's lanes. A body node in no lane is in the
// subprocess's own lane.
func (s *CompileState) ShareSegmentsWith(parent *CompileState, subProcessID string) {
	s.sd = parent.sd
	s.Called = parent.Called
	s.defaultLane, s.defaultIAMDeptID = parent.laneOf(subProcessID)
}

func (s *CompileState) CollectedDepts() []dsl.DepartmentDef { return s.sd.depts }

func (s *CompileState) CollectedSteps() []dsl.ExecutionStep { return s.steps }

func (s *CompileState) AppendStep(step dsl.ExecutionStep) {
	s.steps = append(s.steps, step)
}

func (s *CompileState) StepsCount() int { return len(s.steps) }

func (s *CompileState) PatchStep(idx int, f func(*dsl.ExecutionStep)) {
	if idx >= 0 && idx < len(s.steps) {
		f(&s.steps[idx])
	}
}

func (s *CompileState) IsVisited(nodeID string) bool { return s.visited[nodeID] }

func (s *CompileState) SeqBufCount() int { return len(s.seqBuf) }

func (s *CompileState) RevertBranches(gatewayID string, backTargets []string) []dsl.ExclusiveBranch {
	return s.revertBranches(gatewayID, backTargets)
}

func (s *CompileState) TraverseBranches(splitID, joinID string) ([]dsl.ParallelBranch, error) {
	return s.traverseBranches(splitID, joinID)
}

func (s *CompileState) TraverseExclusiveBranches(splitID, joinID string) ([]dsl.ExclusiveBranch, error) {
	return s.traverseExclusiveBranches(splitID, joinID)
}

func (s *CompileState) laneOf(nodeID string) (name, iamDeptID string) {
	if lane := LaneFor(nodeID, s.Proc); lane != nil {
		return lane.Name, LaneDeptID(lane)
	}
	return s.defaultLane, s.defaultIAMDeptID
}

// SegmentOf returns the department nodeID's stage goes into: the segment
// open on this state's path when nodeID is in its lane, otherwise a segment
// of nodeID's lane that it opens. That is the lane's latest segment while it
// holds no stage, made only for a call to bind, or else the lane's next.
func (s *CompileState) SegmentOf(nodeID string) string {
	lane, iamDeptID := s.laneOf(nodeID)
	if s.openSegment == "" || s.openLane != lane || s.segmentStarts[nodeID] {
		latest := segmentID(lane, s.sd.segments[lane])
		if i, ok := s.sd.deptIdx[latest]; !ok || s.sd.segments[lane] == 0 || len(s.sd.depts[i].Stages) > 0 {
			s.sd.segments[lane]++
			latest = segmentID(lane, s.sd.segments[lane])
			s.EnsureDept(latest, lane, iamDeptID)
		}
		s.openLane, s.openSegment = lane, latest
	}
	if _, placed := s.sd.nodeSegment[nodeID]; !placed {
		s.sd.nodeSegment[nodeID] = s.openSegment
	}
	s.pathSegment[nodeID] = s.openSegment
	return s.openSegment
}

// LaneDepartment returns a department of nodeID's lane for a call to bind,
// without opening it: the lane's first segment, made if the lane has none.
func (s *CompileState) LaneDepartment(nodeID string) string {
	lane, iamDeptID := s.laneOf(nodeID)
	return s.NamedLaneDepartment(lane, iamDeptID)
}

// NamedLaneDepartment is LaneDepartment for the lane called lane.
func (s *CompileState) NamedLaneDepartment(lane, iamDeptID string) string {
	if s.sd.segments[lane] == 0 {
		s.sd.segments[lane] = 1
		s.EnsureDept(lane, lane, iamDeptID)
	}
	return lane
}

// MarkNodeSegment records the department a node that holds no stage of its
// own, a subprocess, starts in, so a boundary or revert to it can name it.
func (s *CompileState) MarkNodeSegment(nodeID, deptID string) {
	if deptID == "" {
		return
	}
	if _, placed := s.sd.nodeSegment[nodeID]; !placed {
		s.sd.nodeSegment[nodeID] = deptID
	}
	s.pathSegment[nodeID] = deptID
}

func segmentID(lane string, n int) string {
	if n == 1 {
		return lane
	}
	return fmt.Sprintf("%s%s%d", lane, SegmentSeparator, n)
}

// segmentOfPending is a reference to nodeID's segment, resolved by
// ResolveTargets.
func segmentOfPending(nodeID string) string { return pendingTarget + nodeID }

// ResolveTargets replaces every pending reference in the departments and the
// steps with the segment of the node it names, or with the node's lane when
// the node holds no stage.
func (s *CompileState) ResolveTargets() {
	resolve := func(ref *string) {
		node, pending := strings.CutPrefix(*ref, pendingTarget)
		if !pending {
			return
		}
		if seg, ok := s.sd.nodeSegment[node]; ok {
			*ref = seg
			return
		}
		*ref, _ = s.laneOf(node)
	}
	for i := range s.sd.depts {
		for j := range s.sd.depts[i].Stages {
			st := &s.sd.depts[i].Stages[j]
			if st.BoundaryTimer != nil {
				resolve(&st.BoundaryTimer.TargetDept)
				st.BoundaryTimer.Steps = s.pathSteps(st.BoundaryTimer.BoundaryID, resolve)
			}
			if st.BoundaryMessage != nil {
				resolve(&st.BoundaryMessage.TargetDept)
				st.BoundaryMessage.Steps = s.pathSteps(st.BoundaryMessage.BoundaryID, resolve)
			}
		}
	}
	s.resolveStepTargets(s.steps, resolve)
}

// pathSteps is the path boundaryID leads to, its pending references resolved.
func (s *CompileState) pathSteps(boundaryID string, resolve func(*string)) []dsl.ExecutionStep {
	steps := s.sd.boundarySteps[boundaryID]
	s.resolveStepTargets(steps, resolve)
	return steps
}

func (s *CompileState) resolveStepTargets(steps []dsl.ExecutionStep, resolve func(*string)) {
	paths := func(errs []dsl.ErrorPath, timers []dsl.TimerPath, msgs []dsl.MessagePath) {
		for i := range errs {
			resolve(&errs[i].TargetDept)
			errs[i].Steps = s.pathSteps(errs[i].BoundaryID, resolve)
		}
		for i := range timers {
			resolve(&timers[i].TargetDept)
			timers[i].Steps = s.pathSteps(timers[i].BoundaryID, resolve)
		}
		for i := range msgs {
			resolve(&msgs[i].TargetDept)
			msgs[i].Steps = s.pathSteps(msgs[i].BoundaryID, resolve)
		}
	}
	for i := range steps {
		st := &steps[i]
		paths(nil, nil, st.MessagePaths)
		for j := range st.Parallel {
			s.resolveStepTargets(st.Parallel[j].Steps, resolve)
		}
		for j := range st.Exclusive {
			resolve(&st.Exclusive[j].RevertToDept)
			s.resolveStepTargets(st.Exclusive[j].Steps, resolve)
		}
		if sw := st.SubWorkflow; sw != nil {
			paths(sw.ErrorPaths, sw.TimerPaths, sw.MessagePaths)
			s.resolveStepTargets(sw.Plan.Steps, resolve)
		}
		if cp := st.CallPlan; cp != nil {
			paths(cp.ErrorPaths, cp.TimerPaths, cp.MessagePaths)
		}
	}
}

// FirstDepartment is the first department steps run, looking into a leading
// gateway's first branch that runs one and a leading subprocess's body, or
// empty when they start with a call.
func FirstDepartment(steps []dsl.ExecutionStep) string {
	if len(steps) == 0 {
		return ""
	}
	first := steps[0]
	switch {
	case len(first.Sequential) > 0:
		return first.Sequential[0]
	case first.SubWorkflow != nil:
		return FirstDepartment(first.SubWorkflow.Plan.Steps)
	}
	for _, b := range first.Parallel {
		if dept := FirstDepartment(b.Steps); dept != "" {
			return dept
		}
	}
	for _, b := range first.Exclusive {
		if dept := FirstDepartment(b.Steps); dept != "" {
			return dept
		}
	}
	return ""
}
