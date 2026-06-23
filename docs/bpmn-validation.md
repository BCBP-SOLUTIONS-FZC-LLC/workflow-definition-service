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
- **Compile** (`bpmn_compiler/bpmncore/compile.go` + `element/`) — walks the forward graph to emit departments, stages, and execution steps (sequential / parallel / exclusive, plus error paths for guarded subprocesses). Each BPMN node type is handled by a dedicated `ElementHandler` in `element/`.
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

### Metadata (per `<bpmn:userTask>`)

| Rule | Error code |
| --- | --- |
| `zeebe:taskDefinition` present | `MISSING_TASK_DEFINITION` |
| `taskDefinition.type` matches a registered `StageTypeHandler` | `INVALID_TASK_DEFINITION_TYPE` |
| Task appears in exactly one lane via `<bpmn:flowNodeRef>` | `TASK_NOT_IN_LANE` |
| `zeebe:assignmentDefinition` present | `MISSING_ASSIGNMENT_DEFINITION` |
| `candidateGroups` present, ≤ 256 chars | `CANDIDATE_GROUPS_EMPTY` |
| `candidateUsers` is a single valid UUID v7 | `INVALID_CANDIDATE_USER` |
| `requires_comment` (optional) parses as bool | `INVALID_ZEEBE_PROPERTY` |

### Sequence flow conditions

| Rule | Error code |
| --- | --- |
| `conditionExpression` on flows leaving an XOR split, ≤ 4096 chars | `INVALID_CONDITION_EXPRESSION` |

### Timer boundary events

| Rule | Error code |
| --- | --- |
| Timer duration is valid ISO 8601 or Go duration | `INVALID_SLA_DURATION` |
| At most one timer boundary per task | (structural: `DANGLING_NODE` / `UNREACHABLE_NODE`) |

### Topological — guarded loops

Cycles are not rejected outright; rework/revert loops are allowed when *guarded* (see CLAUDE.md decision for the full algorithm):

| Rule | Error code |
| --- | --- |
| Every back-edge originates at a diverging exclusive gateway that keeps a forward exit | `UNGUARDED_LOOP` |
| Every multi-node strongly-connected component has a guarded exit | `CYCLE_DETECTED` |

Back-edges are classified once via DFS from the start event (`classifyBackEdges`). A guarded loop compiles to an `exclusive` step whose revert branches carry `revert_to_dept` / `revert_to_stage`.

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
`INVALID_TASK_DEFINITION_TYPE`, `TASK_NOT_IN_LANE`,
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
`INVALID_SEQUENCE_FLOW_REF`, `MULTIPLE_PROCESSES`.

!!! note "Unparseable documents"
    Documents that fail XML parsing (bad XML, `DOCTYPE`/entity injection, token-cap exceeded) return **400 `INVALID_BPMN_XML`** — not a BPMN validation code. Forbidden `DOCTYPE`/entity additionally triggers an internal security log with client IP and tenant ID; the client sees only the generic 400.
