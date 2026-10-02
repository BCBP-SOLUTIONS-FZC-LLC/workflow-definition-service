---
name: bpmn-compiler
description: BPMN parsing, validation, compilation rules and error codes for workflow-definition-service
metadata:
  type: reference
---

# BPMN Compiler Reference

The compiler (`internal/bpmn_compiler/`) is stateless. It imports `internal/core/domain` for error types and `github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-models/pkg/dsl` for compiled-plan structs (`CompiledPlan`, `StageDef`, `ExecutionStep`, etc. — `internal/core/domain/compiled_plan.go` no longer exists), but nothing from `adapter/` or `service/`. Three phases: **parse → validate → compile**. A separate `Hash(ctx, bpmnXML) (string, error)` method computes the canonical SHA-256 of a BPMN document for change-detection without a full compile.

## Error Response Codes

| Phase | Trigger | HTTP | Error code |
|---|---|---|---|
| Parse | Unparseable XML, token cap exceeded | 400 | `INVALID_BPMN_XML` |
| Parse | `DOCTYPE` / entity injection detected | 400 | `INVALID_BPMN_XML` (detection not disclosed) |
| Validate | Structural / semantic / topological errors | 422 | `BPMN_VALIDATION_FAILED` |

`ErrMalformedBPMN` marks unparseable XML. `ErrForbiddenXML` tags XXE attempts — the client sees the same 400 but `logForbiddenXML` emits an internal `Warn` with client IP + tenant. `REJECTED_ELEMENT` is emitted by a raw token scan (forbidden elements don't survive `encoding/xml` unmarshal).

**Parser limits:** tasks 1000, lanes 100, candidateGroups 256 chars, path depth 2000, token cap 1 000 000.

## Parse Behavior

Beyond XXE/entity-injection guarding and namespace checks, the parser applies the following normalisations during unmarshal:

- **Empty-ref sequence flows stripped**: sequence flows with an empty `sourceRef` or `targetRef` are silently discarded before the graph is built. These are orphan artefacts produced by bpmnjs when a connection is partially deleted; retaining them would cause spurious `INVALID_SEQUENCE_FLOW_REF` errors.
- **Message boundary events with no outgoing flow**: a `<bpmn:boundaryEvent>` carrying a `messageEventDefinition` and zero outgoing sequence flows is **valid** — it is treated as a terminal interrupt notification. Timer and error boundary events still require at least one outgoing flow.

## Guarded BPMN Loops

Cycles are not rejected outright. `classifyBackEdges` does a DFS; back-edges are permitted only when guarded:

- Every back-edge must originate at an **exclusive gateway** with a forward exit.
- Every multi-node SCC must contain such a gateway — else `UNGUARDED_LOOP` / `CYCLE_DETECTED`.
- Exception: a `receiveTask` may be a back-edge target without a preceding XOR gateway (it is externally guarded by the inbound message).

The compiler walks **forward edges only** (skipping back-edges) so traversal always terminates. `validateMaxDepth` caps forward path length at 2000.

All graph traversal is **iterative** (`bpmncore.IterativeDFS` + `DFSVisitor{OnEnter, OnExit}`). `OnEnter` returns false to prune children; `OnExit` enables onStack tracking for back-edge classification. Used by `ClassifyBackEdges`, `ValidateReachability`, and `ValidateMaxDepth`.

## Department Derived from Lane Membership

The compiler places a task by the lane it appears in via `<bpmn:flowNodeRef>`. The lane's `dept_id` property is its IAM department. Tasks run in the order the flows reach them: a lane says who does a task, never when.

- **A department is a lane segment**, a stretch of one lane on one path (`CompileState.SegmentOf`). The traversal starts the lane's next segment when the lane changes or any step other than `Sequential` comes between, and a gateway branch or a boundary path starts its own. A lane's first segment is keyed by its name, later ones `<lane>~<n>`; `Label` is always the lane name. A subprocess body shares its parent's segments; a called process numbers its own.
- **Gateways.** The join is the split's immediate post-dominator in the forward graph (`FindJoin`). Each forward branch compiles on its own state, up to the join, into its own steps: `ParallelBranch.Steps` and `ExclusiveBranch.Steps`.
- **Targets by node.** A boundary or revert names a node; `ResolveTargets` replaces it with the node's segment once the compile is done.
- Task not listed in any lane → `TASK_NOT_IN_LANE`; a lane name with `/`, `::` or `~` → `INVALID_LANE_NAME`
- A parallel split with no join, or an exclusive one whose branches do not all end, is `UNMATCHED_GATEWAY`

See [ARCHITECTURE.md § BPMN Compiler](../ARCHITECTURE.md#bpmn-compiler).

## Stage Type + Assignment Elements

**Stage type** comes from `<zeebe:taskDefinition type="prep|review|approve"/>` (not a custom Zeebe property). Allowed values are resolved via the `StageTypeHandler` registry — **not hardcoded** — so the set can be extended without a compiler change. Unregistered types emit `UNKNOWN_STAGE_TYPE` at warning severity (compilation continues with an `engine_note`); `INVALID_TASK_DEFINITION_TYPE` is deprecated.

**Boundary node types**: `NodeTypeTimerBoundaryEvent`, `NodeTypeErrorBoundaryEvent`, and `NodeTypeMessageBoundaryEvent` are separate constants (all subtypes of the legacy `NodeTypeBoundaryEvent`). These drive different element handlers and validator paths. Timer and message boundaries attach to a `userTask` (`StageDef.BoundaryTimer`/`BoundaryMessage`); timer, message and error boundaries attach to a `subProcess` (`SubWorkflowStep.*Paths`) and a `callActivity` (`CallPlanStep.*Paths`). `BoundaryTarget` gives each one the segment of the first task or subProcess on its path, or `Terminates` when an end event comes first; a path starting at a call or a message task, or at a subProcess whose body does not start with a userTask, is `UNSUPPORTED_BOUNDARY_TARGET`. A boundary's path compiles on its own state, so its tasks never join the main steps. A laneless process's nodes are in its implicit slot, and a subprocess body's in the subprocess's lane, so no target is ever empty unless it terminates.

**Canonical hash** (`hasher.go`): `canonicalHash` hashes the normalized process for `artifact_hash`, with the zeebe properties of top-level tasks and sequence flows sorted by name, copying the slices it sorts so the parsed struct is not mutated. The resulting SHA-256 hex is used by `Hash()`. A bundle with several processes (a called process, bundled modules) is hashed by `canonicalBundleHash`: each process hashed on its own, ordered by (id, hash), then hashed together. A single process's hash is exactly `canonicalHash`.

**`zeebe:taskSchedule`**: optional `<zeebe:taskSchedule dueDate="…" followUpDate="…"/>` on `userTask` is extracted into `StageDef.DueDate` and `StageDef.FollowUpDate` (both strings, omitted from JSON when absent). Values are treated as opaque FEEL expressions; the compiler does not validate them.

**Role and default assignees** come from `<zeebe:assignmentDefinition candidateGroups="…" candidateUsers="…"/>` (standard Camunda 8 extension elements). `candidateUsers` is a single UUID v7 (enforced via a named validation function; will be relaxed to multi-assignee later). Eligibility of `candidateGroups` is delegated to `MembershipClient.CheckEligibility` at publish time.

## XOR Gateways vs Error Events

`<bpmn:conditionExpression>` on XOR outgoing flows is for **explicit routing decisions** (bid vs no-bid, value thresholds, path selection).

**Approval-stage rejection** is modelled with error events: the approve-stage worker signals an error code on rejection; an error boundary event on the enclosing subprocess catches it and routes the rework path. This keeps XOR semantics clean (deterministic choice) vs error semantics (exception outcome).

See [ARCHITECTURE.md § BPMN Compiler](../ARCHITECTURE.md#bpmn-compiler) §Routing vs Rejection.

## One Pool Runs; Ignored Pools and Lanes

- **The root** is the one process that is executable, not called and not marked ignore (`runnableRoot`, `executableProcesses` in `compiler.go`): two are `MULTIPLE_PROCESSES`, none `NO_RUNNABLE_PROCESS`. Every other uncalled pool is an outside party's and produces nothing; its calls are not counted (`executableProcesses`) or followed (`ValidateCallGraph`). In a collaboration, the root may lack a start event (`FindImplicitStart`).
- **A called process always runs as a module**, executable or not. A call to a pool marked ignore is `UNRESOLVED_CALLED_ELEMENT`, so ignoring can never drop a module.
- **An ignored lane** (`bpmncore.IsIgnoredLane`) is an outside party inside a process that runs. Its user, send and receive tasks (`bpmncore.SkippedTasks`) are pass-through nodes in `TraverseNode`, `taskAhead` and `firstTaskNodeAhead`, exempt from the per-element checks, passed over by `validateBoundaryTarget`, not waited on by `ValidateCollaboration` (which warns only for receive tasks and message boundaries that run), and not module slots (`WorkLanes`). A call, subprocess or connector task in one, a boundary on a skipped task, and a back-edge whose way back passes no task that runs (`loopRunsWork`) are `IGNORED_LANE_WORK` (`validator/ignored_lane.go`); a `Depts` value naming an ignored caller lane is `INVALID_ZEEBE_PROPERTY`.

## callActivity: referenced plans

Each called process compiles **once, into its own plan** in `CompiledCollaboration.Plans`, named by its process id, and each `<bpmn:callActivity>` compiles to an `ExecutionStep.CallPlan` naming it. Consumers never interpret `CallPlan` directly: they run `dsl.ExpandCalls` (workflow-models), which turns each call into a `SubWorkflowStep` and clones the called plan's departments into the caller as `<call id>::<lane>`. Definition does this for assignee extraction, eligibility, divergence and the exact stage count at publish; Execution does it when it decodes a plan.

- **Open and fixed lanes.** A module lane that holds work (a task, a callActivity or a subProcess) becomes a department. With a `dept_id` property it is fixed, and `Depts` cannot remap it. Without one it is open, and is the only kind of lane allowed to lack `dept_id` (`BPMNDefinitions.ModuleProcesses` marks the processes where that applies). A laneless module is one open lane, whose department ID is the process id without the plan name's `@v<n>` (`bpmncore.ImplicitSlot`), so its tasks' node keys stay the same across module versions.
- **Binding.** `Depts` (JSON object of strings, plain or `=`-prefixed) maps open lanes to caller lanes, case-insensitively; a single open lane left unmapped takes the callActivity's own lane. `Assignees` maps module task ids, or `::`-joined paths of calls to a task in a nested module, to users for this call; `ExpandCalls` lets the outermost call's entry win. Both are stripped from `IOMapping`. In a workflow (not a module), a binding to a lane with no `dept_id` is `MISSING_DEPT_ID`; user ids must be UUID v7 (`validator.IsUserID`).
- **Boundaries.** Message, timer and error boundary events on a callActivity compile to the call step's paths, built the same way as a subProcess's.
- **Library modules.** `ModuleCalls` lists the calls that name a library module, with their pinned `versionTag` (0 = latest): every call with a `versionTag`, since only library modules have versions, and every other call whose target is not a process in the document. Whether a call is a library call is judged in the document it was written in, so a module's call never takes over a workflow's local process of the same id. It flags a FEEL `processId`, a `versionTag` that is not a positive integer, an unknown `bindingType`, `deployment` with no local process, and a `versionTag` pinning a process id the file also defines (other than a module pinning an earlier version of itself), all as `UNRESOLVED_CALLED_ELEMENT`. The service resolves those calls (`service/module_resolution.go`) and `BundleModules` merges each resolved version under its plan name `<process_id>@v<n>`, rewriting the calls that name it and clearing their binding attributes. `bundleDefinitions` brings the module's `<bpmn:message>`/`<bpmn:error>` definitions along as `<plan name>/<id>` and points the module's references at them, so a module keeps its own message names and error codes. Diagrams in module BPMNs are intentionally NOT merged.
- **Golden plans.** `test/unit/service/golden_test.go` publishes the `golden-library-*.bpmn` fixtures and requires each stored plan to equal its workflow-models golden (`dsltest.LibraryCalls`, `dsltest.LibraryCallBoundaries`), which Execution's tests run (workflow-models LLD §2.7). When a compiler change alters one on purpose, regenerate them with `go test ./test/unit/service -run TestGolden -update-golden` under a Go workspace that uses a workflow-models checkout, and commit the new goldens there.
- **Module content.** `ValidateModule` validates a bundled module with its own process as the root and its open lanes allowed, skipping the diagram rules. For a global module, a task naming a user is `INVALID_CANDIDATE_USER`.

**Nested `<bpmn:subProcess>`** inside another subProcess is not supported — the validator emits `NESTED_SUBPROCESS_NOT_SUPPORTED`.

## Module BPMNs (Called Processes)

- Each called process reachable from the root is validated once with the root's rules, minus the diagram rules (`BundleModules` drops module diagrams). A call cycle is `CALLED_ELEMENT_CYCLE`, and a repeated process `id` is `DUPLICATE_PROCESS_ID` (`validator/call_graph.go`).
- `CANDIDATE_USERS_EMPTY` is a warning on a document and an error on a workflow's expanded plan: `CompileForPublish` (`checkExpanded`) refuses every human stage with no default user, on the task or on the top-level call that runs it, naming the node key. `ValidateForPublish`, which `/workflows/validate` uses, runs the same check; workflow and starter creation run `Validate` alone, so a draft can be saved first. `CompileDraft` compiles without these publish-only rules (default users, IAM departments via `lanelessWork`/`unplacedTasks`, the expanded task limit); the diff and the divergence baseline use it.

## Publish Shape

- **Publish always stores a `CompiledCollaboration`**, built by `CompileForPublish`: the one runnable pool becomes the main plan, `main_plan` is the process name (or its id when unnamed), each called process is a plan beside it, and `schema_version` is stamped there. A collaboration publishes the same way; outside parties' pools produce nothing.

**Message name resolution** (`ResolveMessageFlowName`, `bpmncore/compile.go`): real-world BPMN rarely annotates the `<bpmn:messageFlow>` element itself with `name`/`messageRef` — the messageRef almost always lives on the connected node instead. Resolution order: the flow's own `name` → the flow's own `messageRef` (via root `<bpmn:message>`) → the target node's `messageRef` (`nodeMessageRef`: sendTask/receiveTask attribute, or boundary event's `messageEventDefinition`) → the same lookup on the source node. `ResolveMessageFlowTarget` provides the inverse (node ID → message name) for a receive task or boundary whose own `messageRef` doesn't resolve; a receive task with neither waits for its own id. A flow into the root with no name gets `MISSING_MESSAGE_DEFINITION` as a warning from `validator/collab.go`'s `ValidateCollaboration`.
