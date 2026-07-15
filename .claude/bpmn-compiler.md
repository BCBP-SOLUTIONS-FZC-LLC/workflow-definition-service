---
name: bpmn-compiler
description: BPMN parsing, validation, compilation rules and error codes for workflow-definition-service
metadata:
  type: reference
---

# BPMN Compiler Reference

The compiler (`internal/bpmn_compiler/`) is stateless. It imports `internal/core/domain` for error types and compiled-plan structs but nothing from `adapter/` or `service/`. Three phases: **parse → validate → compile**. A separate `Hash(ctx, bpmnXML) (string, error)` method computes the canonical SHA-256 of a BPMN document for change-detection without a full compile.

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

**Canonical hash** (`hasher.go`): `canonicalHash` shallow-copies all 14 slice fields of `BPMNProcess` via `copySlice[T]` before sorting and marshalling to prevent mutating the parsed struct. The resulting SHA-256 hex is used by `Hash()`.

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

## callActivity Flat Compilation

`<bpmn:callActivity>` steps are **flattened directly into the parent execution plan** — no `SubWorkflowStep` wrapper is emitted. `SubWorkflowStep` is exclusively for inline `<bpmn:subProcess>` elements.

The called process brings its own lanes/departments. The parent lane of the callActivity node is visual-only and is NOT inherited by the called process. Departments from the called process appear alongside the parent's departments in the compiled plan.

**Timer boundary events on callActivity are not supported** — rejected at compile time. Error boundary events on callActivity pass validation but also fail compilation. Neither type produces a `BoundaryTimer` in the compiled output.

Called processes must be supplied as entries in `module_bpmn_xmls` on the draft. The service merges them in-memory before compile/validate via `Bundle()`. Diagrams in module BPMNs are intentionally NOT merged.

**Nested `<bpmn:subProcess>`** inside another subProcess is not supported — the validator emits `NESTED_SUBPROCESS_NOT_SUPPORTED`.

## Module BPMNs (Called Processes)

When a called process has **no lanes**, the callActivity must include `<zeebe:ioMapping>` with a `dept_id` input. This input determines which department in the compiled plan the module's tasks are assigned to. The validator emits `MISSING_DEPT_INPUT_FOR_MODULE` if it's absent.

- `dept_id` is compiler-internal — it is NOT emitted in the compiled `ExecutionStep.IOMapping`.
- All other inputs/outputs in `zeebe:ioMapping` ARE emitted in `ExecutionStep.IOMapping` for the Execution Service.
- When the called process has its own lanes, the caller does NOT provide `dept_id` — the module uses its own lane names as department IDs.

## Multi-Participant Collaboration

The compiler accepts `<bpmn:collaboration>` with multiple `<bpmn:participant>` elements:

- Each `isExecutable="true"` participant process compiles to a separate `CompiledPlan` wrapped in a `CompiledCollaboration`.
- Message flows (`<bpmn:messageFlow>`) compile to `MessageDef` entries linking plans by message name.
- Non-executable participants (external-party context only) are parsed for structural integrity but produce no compiled plan.
- Single-process BPMN (no collaboration wrapper) continues to compile to `*CompiledPlan` directly — no breaking change.

**Message name resolution** (`ResolveMessageFlowName`, `bpmncore/compile.go`): real-world BPMN rarely annotates the `<bpmn:messageFlow>` element itself with `name`/`messageRef` — the messageRef almost always lives on the connected node instead. Resolution order: the flow's own `name` → the flow's own `messageRef` (via root `<bpmn:message>`) → the target node's `messageRef` (`nodeMessageRef`: sendTask/receiveTask attribute, or boundary event's `messageEventDefinition`) → the same lookup on the source node. `ResolveMessageFlowTarget` provides the inverse (node ID → message name) for boundary-event resolution when the boundary's own `messageRef` doesn't resolve. If nothing resolves, `validator/collab.go`'s `ValidateCollaboration` emits `MISSING_MESSAGE_DEFINITION` as a warning — the flow still compiles with an empty name.
