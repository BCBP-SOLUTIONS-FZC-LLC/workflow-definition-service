package service

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
)

type moduleResolver struct {
	compiler port.PlanCompiler
	versions port.ModuleVersionRepository
}

type moduleLookup func(ctx context.Context, processID string, version int32) (*domain.ModuleVersion, error)

func (r moduleResolver) live(tenantID uuid.UUID) moduleLookup {
	return func(ctx context.Context, processID string, version int32) (*domain.ModuleVersion, error) {
		v, err := r.versions.Resolve(ctx, tenantID, processID, version)
		if err != nil {
			return nil, fmt.Errorf("resolve module %q: %w", processID, err)
		}
		return v, nil
	}
}

func (r moduleResolver) snapshot(tenantID uuid.UUID, refs []domain.ModuleRef) moduleLookup {
	return func(ctx context.Context, processID string, version int32) (*domain.ModuleVersion, error) {
		var pick *domain.ModuleRef
		for i := range refs {
			ref := &refs[i]
			if ref.ProcessID != processID {
				continue
			}
			if version == ref.VersionNumber || (version == 0 && (pick == nil || ref.VersionNumber > pick.VersionNumber)) {
				pick = ref
			}
		}
		if pick == nil {
			return nil, domain.ErrNotFound
		}
		v, err := r.versions.GetByID(ctx, tenantID, pick.VersionID)
		if err != nil {
			return nil, fmt.Errorf("get module version %s: %w", pick.VersionID, err)
		}
		return v, nil
	}
}

type resolution struct {
	bundled string
	refs    []domain.ModuleRef
	issues  []domain.BPMNValidationError
}

type moduleKey struct {
	processID string
	version   int32
}

type pendingDoc struct {
	xml       string
	versionID uuid.UUID
}

type moduleEdge struct {
	from  uuid.UUID
	call  string
	label string
}

type moduleRoute struct {
	rootCall string
	path     string
}

func (r moduleResolver) resolve(ctx context.Context, lookup moduleLookup, mainXML, self string) (*resolution, error) {
	w := &moduleWalk{
		lookup: lookup, self: self,
		refs: map[uuid.UUID]domain.ModuleRef{}, resolvedTo: map[moduleKey]*domain.ModuleVersion{}, failed: map[moduleKey]bool{},
		reachedBy: map[uuid.UUID][]moduleEdge{}, docIssues: map[uuid.UUID][]domain.BPMNValidationError{},
		queue: []pendingDoc{{xml: mainXML}},
	}
	for len(w.queue) > 0 {
		doc := w.queue[0]
		w.queue = w.queue[1:]
		calls, callIssues, err := r.compiler.ModuleCalls(ctx, doc.xml)
		if err != nil {
			return nil, fmt.Errorf("list module calls: %w", err)
		}
		for _, e := range callIssues {
			w.report(doc, e)
		}
		for _, call := range calls {
			if err := w.visit(ctx, doc, call); err != nil {
				return nil, err
			}
		}
	}
	w.attributeModuleIssues()
	if len(w.issues) > 0 {
		return &resolution{issues: w.issues}, nil
	}
	bundled, err := r.compiler.BundleModules(mainXML, w.modules)
	if err != nil {
		return nil, fmt.Errorf("bundle modules: %w", err)
	}
	out := &resolution{bundled: bundled}
	for _, ref := range w.refs {
		out.refs = append(out.refs, ref)
	}
	slices.SortFunc(out.refs, func(a, b domain.ModuleRef) int {
		return cmp.Or(cmp.Compare(a.ProcessID, b.ProcessID), cmp.Compare(a.VersionNumber, b.VersionNumber))
	})
	return out, nil
}

type moduleWalk struct {
	lookup     moduleLookup
	self       string
	modules    []domain.ResolvedModule
	issues     []domain.BPMNValidationError
	refs       map[uuid.UUID]domain.ModuleRef
	resolvedTo map[moduleKey]*domain.ModuleVersion
	failed     map[moduleKey]bool
	queue      []pendingDoc
	reachedBy  map[uuid.UUID][]moduleEdge
	docIssues  map[uuid.UUID][]domain.BPMNValidationError
	issueOrder []uuid.UUID
}

func (w *moduleWalk) visit(ctx context.Context, doc pendingDoc, call domain.ModuleCall) error {
	if w.self != "" && call.ProcessID == w.self && call.VersionNumber == 0 {
		w.report(doc, selfCall(call, w.self))
		return nil
	}
	key := moduleKey{call.ProcessID, call.VersionNumber}
	if w.failed[key] {
		w.report(doc, unresolvedCall(call))
		return nil
	}
	if v, ok := w.resolvedTo[key]; ok {
		w.reach(doc, call, v)
		return nil
	}
	v, err := w.lookup(ctx, call.ProcessID, call.VersionNumber)
	if errors.Is(err, domain.ErrNotFound) {
		w.failed[key] = true
		w.report(doc, unresolvedCall(call))
		return nil
	}
	if err != nil {
		return err
	}
	w.resolvedTo[key] = v
	w.reach(doc, call, v)
	number := *v.VersionNumber
	w.modules = append(w.modules, domain.ResolvedModule{
		ProcessID:     call.ProcessID,
		VersionNumber: call.VersionNumber,
		PlanName:      fmt.Sprintf("%s@v%d", v.ProcessID, number),
		BPMNXML:       v.BPMNXML,
	})
	if _, seen := w.refs[v.ID]; !seen {
		w.refs[v.ID] = domain.ModuleRef{ModuleID: v.ModuleID, VersionID: v.ID, ProcessID: v.ProcessID, VersionNumber: number}
		w.queue = append(w.queue, pendingDoc{xml: v.BPMNXML, versionID: v.ID})
	}
	return nil
}

func (w *moduleWalk) reach(doc pendingDoc, call domain.ModuleCall, v *domain.ModuleVersion) {
	edge := moduleEdge{from: doc.versionID, call: call.CallID, label: fmt.Sprintf("module %q version %d", v.ProcessID, *v.VersionNumber)}
	if !slices.Contains(w.reachedBy[v.ID], edge) {
		w.reachedBy[v.ID] = append(w.reachedBy[v.ID], edge)
	}
}

func (w *moduleWalk) report(doc pendingDoc, e domain.BPMNValidationError) {
	if doc.versionID == uuid.Nil {
		w.issues = append(w.issues, e)
		return
	}
	if _, seen := w.docIssues[doc.versionID]; !seen {
		w.issueOrder = append(w.issueOrder, doc.versionID)
	}
	w.docIssues[doc.versionID] = append(w.docIssues[doc.versionID], e)
}

func (w *moduleWalk) attributeModuleIssues() {
	routes := map[uuid.UUID][]moduleRoute{}
	for _, versionID := range w.issueOrder {
		for _, route := range w.routes(versionID, routes, map[uuid.UUID]bool{}) {
			for _, e := range w.docIssues[versionID] {
				e.Message = fmt.Sprintf("inside %s: %s", route.path, e.Message)
				e.NodeID = route.rootCall
				w.issues = append(w.issues, e)
			}
		}
	}
}

func (w *moduleWalk) routes(versionID uuid.UUID, memo map[uuid.UUID][]moduleRoute, visiting map[uuid.UUID]bool) []moduleRoute {
	if known, ok := memo[versionID]; ok {
		return known
	}
	if visiting[versionID] {
		return nil
	}
	visiting[versionID] = true
	defer delete(visiting, versionID)
	var out []moduleRoute
	add := func(r moduleRoute) {
		if !slices.ContainsFunc(out, func(o moduleRoute) bool { return o.rootCall == r.rootCall }) {
			out = append(out, r)
		}
	}
	for _, edge := range w.reachedBy[versionID] {
		if edge.from == uuid.Nil {
			add(moduleRoute{rootCall: edge.call, path: edge.label})
			continue
		}
		for _, parent := range w.routes(edge.from, memo, visiting) {
			add(moduleRoute{rootCall: parent.rootCall, path: parent.path + " → " + edge.label})
		}
	}
	memo[versionID] = out
	return out
}

func (r moduleResolver) validate(ctx context.Context, lookup moduleLookup, bpmnXML string) ([]domain.BPMNValidationError, error) {
	return r.validateWith(ctx, lookup, bpmnXML, r.compiler.Validate)
}

func (r moduleResolver) validateForPublish(ctx context.Context, lookup moduleLookup, bpmnXML string) ([]domain.BPMNValidationError, error) {
	return r.validateWith(ctx, lookup, bpmnXML, r.compiler.ValidateForPublish)
}

func (r moduleResolver) validateWith(
	ctx context.Context, lookup moduleLookup, bpmnXML string,
	check func(context.Context, string) ([]domain.BPMNValidationError, error),
) ([]domain.BPMNValidationError, error) {
	res, err := r.resolve(ctx, lookup, bpmnXML, "")
	if err != nil {
		return nil, err
	}
	if len(res.issues) > 0 {
		return res.issues, nil
	}
	errs, err := check(ctx, res.bundled)
	if err != nil {
		return nil, fmt.Errorf("validate bpmn: %w", err)
	}
	return errs, nil
}

func unresolvedCall(call domain.ModuleCall) domain.BPMNValidationError {
	msg := fmt.Sprintf("call names module %q, which is not a published, unarchived module this tenant can call", call.ProcessID)
	if call.VersionNumber > 0 {
		msg = fmt.Sprintf("call names module %q version %d, which does not exist or was never published, or the module is archived", call.ProcessID, call.VersionNumber)
	}
	return domain.BPMNValidationError{Code: domain.BPMNErrUnresolvedCalledElement, NodeID: call.CallID, Message: msg, Severity: domain.SeverityError}
}

func selfCall(call domain.ModuleCall, self string) domain.BPMNValidationError {
	return domain.BPMNValidationError{
		Code:   domain.BPMNErrCalledElementCycle,
		NodeID: call.CallID,
		Message: fmt.Sprintf("call names module %q, the module being saved, at its latest version; once this version is published that is a call to itself. "+
			"Pin an earlier version or remove the call", self),
		Severity: domain.SeverityError,
	}
}
