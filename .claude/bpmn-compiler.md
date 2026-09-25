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
- Exception: `receiveTask` and external-participant bridge nodes may be back-edge targets without a preceding XOR gateway (they are externally guarded by the inbound message or external system).

The compiler walks **forward edges only** (skipping back-edges) so traversal always terminates. `validateMaxDepth` caps forward path length at 2000.

All graph traversal is **iterative** (`bpmncore.IterativeDFS` + `DFSVisitor{OnEnter, OnExit}`). `OnEnter` returns false to prune children; `OnExit` enables onStack tracking for back-edge classification. Used by `ClassifyBackEdges`, `ValidateReachability`, and `ValidateMaxDepth`.

## Department Derived from Lane Membership

The compiler derives a userTask's department from the lane it appears in via `<bpmn:flowNodeRef>`. No `dept_id` Zeebe property is read. The lane `name` attribute becomes both `DepartmentDef.ID` and `DepartmentDef.Label`; the Profile Service owns the format and the compiler trusts it as-is.

- Task not listed in any lane → `TASK_NOT_IN_LANE`
- `validateGatewayMatching` requires every forward branch of a split gateway to reconverge at the matched join — a bypassing branch is `UNMATCHED_GATEWAY`

See [ARCHITECTURE.md § BPMN Compiler](../ARCHITECTURE.md#bpmn-compiler).

## Stage Type + Assignment Elements

**Stage type** comes from `<zeebe:taskDefinition type="prep|review|approve"/>` (not a custom Zeebe property). Allowed values are resolved via the `StageTypeHandler` registry — **not hardcoded** — so the set can be extended without a compiler change. Unregistered types emit `UNKNOWN_STAGE_TYPE` at warning severity (compilation continues with an `engine_note`); `INVALID_TASK_DEFINITION_TYPE` is deprecated.

**Boundary node types**: `NodeTypeTimerBoundaryEvent`, `NodeTypeErrorBoundaryEvent`, and `NodeTypeMessageBoundaryEvent` are separate constants (all subtypes of the legacy `NodeTypeBoundaryEvent`). These drive different element handlers and validator paths. Timer boundary events on `callActivity` are rejected at compile time; error boundary events pass validation but also fail compilation. Message boundary events are supported on plain `userTask`, `subProcess`, and `callActivity` — resolved into `StageDef.BoundaryMessage`, `SubWorkflowStep.MessagePaths`, and `ExecutionStep.MessagePaths` respectively.

**Canonical hash** (`hasher.go`): `canonicalHash` shallow-copies all 14 slice fields of `BPMNProcess` via `copySlice[T]` before sorting and marshalling to prevent mutating the parsed struct. The resulting SHA-256 hex is used by `Hash()`. A bundle with several processes (a called process, bundled modules) is hashed by `canonicalBundleHash`: each process hashed on its own, ordered by (id, hash), then hashed together. A single process's hash is exactly `canonicalHash`.

**Synthetic node IDs** (`compiler.go`): collaboration bridge nodes injected by the compiler receive IDs of the form `__ext__` + 4-byte SHA-256 hex of the originating `sendTask` ID, preventing collisions.

**`zeebe:taskSchedule`**: optional `<zeebe:taskSchedule dueDate="…" followUpDate="…"/>` on `userTask` is extracted into `StageDef.DueDate` and `StageDef.FollowUpDate` (both strings, omitted from JSON when absent). Values are treated as opaque FEEL expressions; the compiler does not validate them.

**Role and default assignees** come from `<zeebe:assignmentDefinition candidateGroups="…" candidateUsers="…"/>` (standard Camunda 8 extension elements). `candidateUsers` is a single UUID v7 (enforced via a named validation function; will be relaxed to multi-assignee later). Eligibility of `candidateGroups` is delegated to `MembershipClient.CheckEligibility` at publish time.

## XOR Gateways vs Error Events

`<bpmn:conditionExpression>` on XOR outgoing flows is for **explicit routing decisions** (bid vs no-bid, value thresholds, path selection).

**Approval-stage rejection** is modelled with error events: the approve-stage worker signals an error code on rejection; an error boundary event on the enclosing subprocess catches it and routes the rework path. This keeps XOR semantics clean (deterministic choice) vs error semantics (exception outcome).

See [ARCHITECTURE.md § BPMN Compiler](../ARCHITECTURE.md#bpmn-compiler) §Routing vs Rejection.

## isExecutable Flag

`isExecutable` on `<bpmn:process>` is actively used by the compiler:

- `isExecutable="false"` → **visual-only pool** (e.g. a Client pool showing only message flows). Skipped entirely by the compiler — not compiled, not usable as a called process.
- `isExecutable="true"` → **real executable process**. Required for all pools with business logic, including module/called processes referenced by callActivity.

The compiler distinguishes root entry-point processes from called processes by whether the process ID appears as a `zeebe:calledElement` target in another process — NOT by this flag. A called process must still have `isExecutable="true"`.

## callActivity: referenced plans

Each called process compiles **once, into its own plan** in `CompiledCollaboration.Plans`, named by its process id, and each `<bpmn:callActivity>` compiles to an `ExecutionStep.CallPlan` naming it. Consumers never interpret `CallPlan` directly: they run `dsl.ExpandCalls` (workflow-models), which turns each call into a `SubWorkflowStep` and clones the called plan's departments into the caller as `<call id>::<lane>`. Definition does this for assignee extraction, eligibility, divergence and the exact stage count at publish; Execution does it when it decodes a plan.

- **Open and fixed lanes.** A module lane that holds work (a task, a callActivity or a subProcess) becomes a department. With a `dept_id` property it is fixed, and `Depts` cannot remap it. Without one it is open, and is the only kind of lane allowed to lack `dept_id` (`BPMNDefinitions.ModuleProcesses` marks the processes where that applies). A laneless module is one open lane, whose department ID is the process id.
- **Binding.** `Depts` (JSON object of strings, plain or `=`-prefixed) maps open lanes to caller lanes, case-insensitively; a single open lane left unmapped takes the callActivity's own lane. `Assignees` maps module task ids to users for this call. Both are stripped from `IOMapping`.
- **Boundaries.** Message, timer and error boundary events on a callActivity compile to the call step's paths, built the same way as a subProcess's.
- **Library modules.** `ModuleCalls` lists the calls that name a library module, with their pinned `versionTag` (0 = latest): every call with a `versionTag`, since only library modules have versions, and every other call whose target is not a process in the document. Whether a call is a library call is judged in the document it was written in, so a module's call never takes over a workflow's local process of the same id. It flags a FEEL `processId`, a `versionTag` that is not a positive integer, an unknown `bindingType`, and `deployment` with no local process, all as `UNRESOLVED_CALLED_ELEMENT`. The service resolves those calls (`service/module_resolution.go`) and `BundleModules` merges each resolved version under its plan name `<process_id>@v<n>`, rewriting the calls that name it and clearing their binding attributes. Diagrams in module BPMNs are intentionally NOT merged.
- **Golden plans.** `test/unit/service/golden_test.go` publishes the `golden-library-*.bpmn` fixtures and requires each stored plan to equal its workflow-models golden (`dsltest.LibraryCalls`, `dsltest.LibraryCallBoundaries`), which Execution's tests run (workflow-models LLD §2.7). When a compiler change alters one on purpose, regenerate them with `go test ./test/unit/service -run TestGolden -update-golden` under a Go workspace that uses a workflow-models checkout, and commit the new goldens there.
- **Module content.** `ValidateModule` validates a bundled module with its own process as the root and its open lanes allowed, skipping the diagram rules. For a global module, a task naming a user is `INVALID_CANDIDATE_USER`.

**Nested `<bpmn:subProcess>`** inside another subProcess is not supported — the validator emits `NESTED_SUBPROCESS_NOT_SUPPORTED`.

## Module BPMNs (Called Processes)

- Each called process reachable from the root is validated once with the root's rules, minus the diagram rules (`BundleModules` drops module diagrams). A call cycle is `CALLED_ELEMENT_CYCLE`, and a repeated process `id` is `DUPLICATE_PROCESS_ID` (`validator/call_graph.go`).
- `CANDIDATE_USERS_EMPTY` is a warning: a task's assignee may come from the instance start request's `override_map`, which Execution requires for any task that resolves to nobody.

## Multi-Participant Collaboration

The compiler accepts `<bpmn:collaboration>` with multiple `<bpmn:participant>` elements:

- Each `isExecutable="true"` participant process compiles to a separate `CompiledPlan` wrapped in a `CompiledCollaboration`.
- Message flows (`<bpmn:messageFlow>`) compile to `MessageDef` entries linking plans by message name.
- Non-executable participants (external-party context only) are parsed for structural integrity but produce no compiled plan.
- Single-process BPMN (no collaboration wrapper) compiles to `*CompiledPlan` through `Compile`.
- **Publish always stores a `CompiledCollaboration`**, built by `CompileForPublish`: a single root becomes a one-plan collaboration, `main_plan` is the process name (or its id when unnamed), and `schema_version` is stamped there. A collaboration BPMN validates but does not publish yet; `CompileCollaboration` has no production caller until the ignored-pool task can be completed in the Execution Service.

**Message name resolution** (`ResolveMessageFlowName`, `bpmncore/compile.go`): real-world BPMN rarely annotates the `<bpmn:messageFlow>` element itself with `name`/`messageRef` — the messageRef almost always lives on the connected node instead. Resolution order: the flow's own `name` → the flow's own `messageRef` (via root `<bpmn:message>`) → the target node's `messageRef` (`nodeMessageRef`: sendTask/receiveTask attribute, or boundary event's `messageEventDefinition`) → the same lookup on the source node. `ResolveMessageFlowTarget` provides the inverse (node ID → message name) for boundary-event resolution when the boundary's own `messageRef` doesn't resolve. If nothing resolves, `validator/collab.go`'s `ValidateCollaboration` emits `MISSING_MESSAGE_DEFINITION` as a warning — the flow still compiles with an empty name.
