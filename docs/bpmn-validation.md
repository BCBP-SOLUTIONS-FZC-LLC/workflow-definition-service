# BPMN Validation & Compilation

The `internal/bpmn_compiler` package tree is the stateless heart of the design-time control plane. It turns an uploaded BPMN 2.0 XML document into an immutable, execution-ready DSL (`domain.CompiledPlan`) — or a structured list of validation errors. It holds no state and touches no I/O; assignee eligibility and persistence happen in the service layer around it.

The package is split into four layers: the root (`bpmn_compiler/`) orchestrates parse → validate → compile → hash; `bpmncore/` provides shared traversal types; `validator/` contains all rule functions; `element/` holds per-node-type compilation handlers.

For the full element reference, extension contract, and examples, see [BPMN Workflow Specification](bpmn-spec.md).

## Pipeline

```text
ParseBPMN ─▶ validate ─▶ compile ─▶ Hash
   │            │           │          │
 XXE/entity   structural   forward    canonical SHA-256
 guard +      / metadata / graph →    of the normalized
 unmarshal    topological  DepartmentDef + ExecutionStep   process
              + guarded     DSL
              loop checks
```

- **Parse** (`bpmn_compiler/parser.go`) — rejects `DOCTYPE`/entity declarations (XXE & billion-laughs), caps the token stream, requires the `bpmn` and `zeebe` namespaces (`MISSING_NAMESPACE`), unmarshals into the typed model, and scans for unsupported elements (`REJECTED_ELEMENT`).
- **Validate** (`bpmn_compiler/validator/validator.go`) — runs every rule below over the *forward graph* (all edges minus DFS-classified back-edges). Returns `[]BPMNValidationError`; the service maps a non-empty list to **HTTP 422** with `is_valid: false`.
- **Compile** (`bpmn_compiler/bpmncore/compile.go` + `element/`) — walks the forward graph to emit departments, stages, and execution steps (sequential / parallel / exclusive, plus error paths for guarded subprocesses). Each BPMN node type is handled by a dedicated `ElementHandler` in `element/`. Non-executable participants are compiled as `Ignored: true` plans included in `CompiledCollaboration` for routing reference; the Execution Service skips them as entry points.
- **Hash** (`bpmn_compiler/hasher.go`) — canonical SHA-256 of the normalized process (Zeebe extension elements sorted) for the `artifact_hash` / structural-divergence check.

## Validation Rules

### Structural

| Rule | Error code |
| --- | --- |
| Root declares the `bpmn` and `zeebe` namespaces | `MISSING_NAMESPACE` |
| No element from the Tier-3 denylist | `REJECTED_ELEMENT` |
| Exactly one start event per process | `NO_START_EVENT` / `MULTIPLE_START_EVENTS` |
| At least one end event per process | `NO_END_EVENT` |
| One `<bpmn:process>` per definitions (unless wrapped in `<bpmn:collaboration>`) | `MULTIPLE_PROCESSES` |
| Every non-event node has an in- and out-edge | `DANGLING_NODE` |
| Sequence flows reference known nodes | `INVALID_SEQUENCE_FLOW_REF` |
| Every node reachable from start (DFS) | `UNREACHABLE_NODE` |
| Each split gateway has a matching join **and every branch reconverges at it** | `UNMATCHED_GATEWAY` |
| Longest forward path ≤ 2000 nodes | `MAX_DEPTH_EXCEEDED` |
| Message flow source/target reference known elements | `UNMATCHED_MESSAGE_FLOW` |
| `messageRef` on flow/task references a declared `<bpmn:message>` | `MISSING_MESSAGE_DEFINITION` |
| Boundary event attached to a legal host node type | `INVALID_BOUNDARY_ATTACHMENT` |
| `<bpmndi:BPMNDiagram>` element present | `MISSING_DIAGRAM` |
| Every node and sequence flow has a corresponding diagram shape/edge | `MISSING_DIAGRAM_SHAPE` |
| `callActivity` has `<zeebe:calledElement processId="…"/>` and the `processId` resolves to a process in the same definitions | `UNRESOLVED_CALLED_ELEMENT` |
| `<bpmn:subProcess>` does not contain a nested `<bpmn:subProcess>` | `NESTED_SUBPROCESS_NOT_SUPPORTED` |

**DANGLING_NODE exceptions:** A `<bpmn:boundaryEvent>` with `messageEventDefinition` and zero outgoing sequence flows is **valid** — it is treated as a terminal interrupt (the message fires, the host activity is cancelled, and no continuation step is emitted); no `DANGLING_NODE` error is raised. Timer and error boundary events with zero outgoing flows continue to emit `DANGLING_NODE`.

A `callActivity` or `subProcess` with no incoming sequence flow is **valid** when it is the only entry node in a process with no explicit `<bpmn:startEvent>` (implicit root). It is exempt from the `DANGLING_NODE` no-incoming check. The compiler identifies the implicit root via `FindImplicitStart`: a single node with no incoming and at least one outgoing sequence flow in a process that has no `startEvent`.

### Metadata — userTask

| Rule | Error code |
| --- | --- |
| `zeebe:taskDefinition` present | `MISSING_TASK_DEFINITION` |
| `taskDefinition.type` matches a registered `StageTypeHandler` | `UNKNOWN_STAGE_TYPE` *(warning — compilation continues)*; `INVALID_TASK_DEFINITION_TYPE` is deprecated |
| Task appears in exactly one lane via `<bpmn:flowNodeRef>` | `TASK_NOT_IN_LANE` |
| `zeebe:assignmentDefinition` present | `MISSING_ASSIGNMENT_DEFINITION` |
| `candidateGroups` present, ≤ 256 chars | `CANDIDATE_GROUPS_EMPTY` |
| `candidateUsers` is a single valid UUID v7 | `INVALID_CANDIDATE_USER` |

### Metadata — sendTask / receiveTask

| Rule | Error code |
| --- | --- |
| Task appears in exactly one lane via `<bpmn:flowNodeRef>` | `TASK_NOT_IN_LANE` |
| `zeebe:assignmentDefinition` — optional; when present, same `candidateGroups`/`candidateUsers` rules as userTask apply | `CANDIDATE_GROUPS_EMPTY` / `INVALID_CANDIDATE_USER` |
| `messageRef` references a declared `<bpmn:message>` (structural rule) | `MISSING_MESSAGE_DEFINITION` |

### Metadata — callActivity (module / called process)

| Rule | Error code |
| --- | --- |
| `<zeebe:calledElement processId="…"/>` present and `processId` resolves to a supplied module process | `UNRESOLVED_CALLED_ELEMENT` |
| Called process has at least one `startEvent` | `NO_START_EVENT` (anchored on the callActivity node ID) |
| Called process has no lanes AND callActivity ioMapping has neither a `dept_id` input nor a `target="Depts"` input containing a valid JSON object mapping lane names to dept IDs | `MISSING_DEPT_INPUT_FOR_MODULE` |
| Timer boundary event attached to a `callActivity` | `INVALID_BOUNDARY_ATTACHMENT` |

Note: error boundary events on `callActivity` pass validation but are rejected at compile time. Message boundary events on `callActivity` are fully supported — they pass validation and compile to `ExecutionStep.message_paths` on the flattened step.

### Sequence flow conditions

| Rule | Error code |
| --- | --- |
| `conditionExpression` on flows leaving an XOR split, ≤ 4096 chars | `INVALID_CONDITION_EXPRESSION` |

### Timer boundary events

| Rule | Error code |
| --- | --- |
| Timer duration is valid ISO 8601 or Go duration | `INVALID_SLA_DURATION` |
| At most one timer boundary per task | (structural: `DANGLING_NODE` / `UNREACHABLE_NODE`) |

### Message boundary events

| Rule | Outcome |
| --- | --- |
| `messageEventDefinition` with zero outgoing sequence flows | Valid — treated as terminal interrupt; no error emitted (see DANGLING_NODE exception above) |
| Message boundary event's message name cannot be resolved from `<bpmn:message>` definitions or collaboration message flows targeting the event's ID | `MISSING_MESSAGE_DEFINITION` *(warning — compilation continues with empty correlation key hint)* |

### Topological — guarded loops

Cycles are not rejected outright; rework/revert loops are allowed when *guarded* (see CLAUDE.md decision for the full algorithm):

| Rule | Error code |
| --- | --- |
| Every back-edge originates at a diverging exclusive gateway that keeps a forward exit | `UNGUARDED_LOOP` |
| Exception: `receiveTask` and external participant nodes may be back-edge targets without a preceding XOR gateway (they are externally guarded by the inbound message / external system) | — |
| Every multi-node strongly-connected component has a guarded exit | `CYCLE_DETECTED` |

Back-edges are classified once via DFS from the start event (`classifyBackEdges`). A guarded loop compiles to an `exclusive` step whose revert branches carry `revert_to_dept` / `revert_to_stage`.

### Warnings

These codes are emitted with `IsWarning: true`. The compiler still returns a compiled plan; HTTP callers receive `is_valid: true` alongside a non-empty `warnings` list.

| Code | Trigger |
| --- | --- |
| `UNKNOWN_STAGE_TYPE` | `taskDefinition.type` does not match a registered `StageTypeHandler` — compilation continues with the raw type string |
| `INVALID_ZEEBE_PROPERTY` | A `target="Depts"` input in `zeebe:ioMapping` is present but its source is not valid JSON or is not a `map[string]string` — the module uses its lane names as-is |
| `MISSING_MESSAGE_DEFINITION` | A message boundary event's message name cannot be resolved from either the `<bpmn:message>` definitions or the collaboration's message flows targeting that boundary event's ID — emitted per event; compilation continues with an empty correlation key hint |
| `MISSING_MESSAGE_DEFINITION` | *(collaboration-level)* A `<bpmn:messageFlow>`'s name cannot be resolved via any of the fallback paths in §Message Flow Name Resolution (docs/bpmn-spec.md) — emitted per flow; `MessageDef.Name` compiles to `""` |

## Department Derivation

A task's department is derived from its lane membership — the lane it is listed under via `<bpmn:flowNodeRef>`. No `dept_id` Zeebe property is needed or read. The lane `name` attribute becomes both `DepartmentDef.ID` and `DepartmentDef.Label`; the format is owned by the Profile Service and trusted as-is.

## Limits

| Constant | Value | Meaning |
| --- | --- | --- |
| `maxUserTasks` | 1000 | user tasks per process (`TASK_LIMIT_EXCEEDED`) |
| `maxLanes` | 100 | lanes per process (`LANE_LIMIT_EXCEEDED`) |
| `maxCandidateGroups` | 256 chars | `candidateGroups` length |
| `maxCondExpr` | 4096 chars | `conditionExpression` length |
| `maxPathDepth` | 2000 | longest forward path, start → end |
| `maxTokens` (parser) | 1,000,000 | XML token-stream cap (XML-bomb guard) |
| body size | 10 MB | request body cap (`PAYLOAD_TOO_LARGE`) |

## Error-Code Reference

All codes the compiler emits, mapped from `internal/core/domain/errors.go` (`BPMNErrorCode`). The compiler never panics on malformed input — every validation failure is one of these codes.

`REJECTED_ELEMENT`, `MISSING_NAMESPACE`,
`MISSING_TASK_DEFINITION`, `MISSING_ASSIGNMENT_DEFINITION`,
`UNKNOWN_STAGE_TYPE` *(warning)*, `INVALID_TASK_DEFINITION_TYPE` *(deprecated)*, `TASK_NOT_IN_LANE`,
`CANDIDATE_GROUPS_EMPTY`, `INVALID_CANDIDATE_USER`,
`INVALID_CONDITION_EXPRESSION`, `INVALID_SLA_DURATION`,
`INVALID_ZEEBE_PROPERTY`,
`MISSING_MESSAGE_DEFINITION`, `UNMATCHED_MESSAGE_FLOW`,
`TASK_LIMIT_EXCEEDED`, `LANE_LIMIT_EXCEEDED`,
`MULTIPLE_START_EVENTS`, `NO_START_EVENT`,
`MULTIPLE_END_EVENTS`, `NO_END_EVENT`,
`DANGLING_NODE`, `UNREACHABLE_NODE`,
`CYCLE_DETECTED`, `UNGUARDED_LOOP`,
`MAX_DEPTH_EXCEEDED`, `UNMATCHED_GATEWAY`,
`INVALID_SEQUENCE_FLOW_REF`, `MULTIPLE_PROCESSES`,
`UNRESOLVED_CALLED_ELEMENT`, `NESTED_SUBPROCESS_NOT_SUPPORTED`,
`MISSING_DEPT_INPUT_FOR_MODULE`, `INVALID_BOUNDARY_ATTACHMENT`,
`MISSING_DIAGRAM`, `MISSING_DIAGRAM_SHAPE`, `UNSUPPORTED_ELEMENT`.

!!! note "Unparseable documents"
    Documents that fail XML parsing (bad XML, `DOCTYPE`/entity injection, token-cap exceeded) return **400 `INVALID_BPMN_XML`** — not a BPMN validation code. Forbidden `DOCTYPE`/entity additionally triggers an internal security log with client IP and tenant ID; the client sees only the generic 400.
