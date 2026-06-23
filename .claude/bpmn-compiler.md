---
name: bpmn-compiler
description: BPMN parsing, validation, compilation rules and error codes for workflow-definition-service
metadata:
  type: reference
---

# BPMN Compiler Reference

The compiler (`internal/bpmn_compiler/`) is stateless. It imports `internal/core/domain` for error types and compiled-plan structs but nothing from `adapter/` or `service/`. Three phases: **parse → validate → compile**.

## Error Response Codes

| Phase | Trigger | HTTP | Error code |
|---|---|---|---|
| Parse | Unparseable XML, token cap exceeded | 400 | `INVALID_BPMN_XML` |
| Parse | `DOCTYPE` / entity injection detected | 400 | `INVALID_BPMN_XML` (detection not disclosed) |
| Validate | Structural / semantic / topological errors | 422 | `BPMN_VALIDATION_FAILED` |

`ErrMalformedBPMN` marks unparseable XML. `ErrForbiddenXML` tags XXE attempts — the client sees the same 400 but `logForbiddenXML` emits an internal `Warn` with client IP + tenant. `REJECTED_ELEMENT` is emitted by a raw token scan (forbidden elements don't survive `encoding/xml` unmarshal).

**Parser limits:** tasks 1000, lanes 100, candidateGroups 256 chars, path depth 2000, token cap 1 000 000.

## Guarded BPMN Loops

Cycles are not rejected outright. `classifyBackEdges` does a DFS; back-edges are permitted only when guarded:

- Every back-edge must originate at an **exclusive gateway** with a forward exit.
- Every multi-node SCC must contain such a gateway — else `UNGUARDED_LOOP` / `CYCLE_DETECTED`.

The compiler walks **forward edges only** (skipping back-edges) so traversal always terminates. `validateMaxDepth` caps forward path length at 2000.

## Department Derived from Lane Membership

The compiler derives a userTask's department from the lane it appears in via `<bpmn:flowNodeRef>`. No `dept_id` Zeebe property is read. The lane `name` attribute becomes both `DepartmentDef.ID` and `DepartmentDef.Label`; the Profile Service owns the format and the compiler trusts it as-is.

- Task not listed in any lane → `TASK_NOT_IN_LANE`
- `validateGatewayMatching` requires every forward branch of a split gateway to reconverge at the matched join — a bypassing branch is `UNMATCHED_GATEWAY`

See [docs/bpmn-spec.md](../docs/bpmn-spec.md).

## Stage Type + Assignment Elements

**Stage type** comes from `<zeebe:taskDefinition type="prep|review|approve"/>` (not a custom Zeebe property). Allowed values are resolved via the `StageTypeHandler` registry — **not hardcoded** — so the set can be extended without a compiler change. Returns `INVALID_TASK_DEFINITION_TYPE` if unregistered.

**Role and default assignees** come from `<zeebe:assignmentDefinition candidateGroups="…" candidateUsers="…"/>` (standard Camunda 8 extension elements). `candidateUsers` is a single UUID v7 (enforced via a named validation function; will be relaxed to multi-assignee later). Eligibility of `candidateGroups` is delegated to `MembershipClient.CheckEligibility` at publish time.

## XOR Gateways vs Error Events

`<bpmn:conditionExpression>` on XOR outgoing flows is for **explicit routing decisions** (bid vs no-bid, value thresholds, path selection).

**Approval-stage rejection** is modelled with error events: the approve-stage worker signals an error code on rejection; an error boundary event on the enclosing subprocess catches it and routes the rework path. This keeps XOR semantics clean (deterministic choice) vs error semantics (exception outcome).

See [docs/bpmn-spec.md](../docs/bpmn-spec.md) §Routing vs Rejection.

## Multi-Participant Collaboration

The compiler accepts `<bpmn:collaboration>` with multiple `<bpmn:participant>` elements:

- Each `isExecutable="true"` participant process compiles to a separate `CompiledPlan` wrapped in a `CompiledCollaboration`.
- Message flows (`<bpmn:messageFlow>`) compile to `MessageDef` entries linking plans by message name.
- Non-executable participants (external-party context only) are parsed for structural integrity but produce no compiled plan.
- Single-process BPMN (no collaboration wrapper) continues to compile to `*CompiledPlan` directly — no breaking change.
