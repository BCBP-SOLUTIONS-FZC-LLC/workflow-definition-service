# BPMN Workflow Specification

This service accepts a restricted subset of BPMN 2.0 XML, validated against this profile before any workflow version can be published. The profile is intentionally narrow: it models human-centred approval workflows across departments and companies, not general-purpose automation.

---

## Namespaces

Two namespaces are **required** on `<bpmn:definitions>` and validated:

```xml
<bpmn:definitions
  xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL"
  xmlns:zeebe="http://camunda.org/schema/zeebe/1.0"
  xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI"
  xmlns:dc="http://www.omg.org/spec/DD/20100524/DC"
  xmlns:di="http://www.omg.org/spec/DD/20100524/DI"
  ...>
```

`bpmn` and `zeebe` missing → `MISSING_NAMESPACE`. Diagram namespaces (`bpmndi`, `dc`, `di`) are expected by modelling tools but not validated by the compiler.

---

## Process Structure

### Single-process upload

```xml
<bpmn:process id="Process_1" isExecutable="true" name="Bid-No-Bid Review">
  ...
</bpmn:process>
```

- `name` is required; becomes `CompiledPlan.Name`.
- `isExecutable="true"` required.
- One `<bpmn:process>` per definitions file when no collaboration is present (`MULTIPLE_PROCESSES` otherwise).

### Multi-participant collaboration

```xml
<bpmn:collaboration id="Collab_1">
  <bpmn:participant id="P_consultant" name="Consultant/Owner"
                    processRef="Process_consultant" />
  <bpmn:participant id="P_contractor" name="Contractor"
                    processRef="Process_contractor" />
  <bpmn:messageFlow id="MF_1" sourceRef="Task_issue_rfq"
                    targetRef="Start_rfq_received" messageRef="Msg_rfq" />
</bpmn:collaboration>
```

**System semantics**: each `isExecutable="true"` participant process compiles to a separate `CompiledPlan`. The compiler wraps them in a `CompiledCollaboration`. Non-executable participants (external parties with no lanes) are parsed for structural integrity but produce no compiled plan. Message flows compile to `MessageDef` entries linking source and target plans by message name; the Execution Service starts each plan and correlates them via the shared message.

#### Message Flow Name Resolution

A `<bpmn:messageFlow>` element is rarely annotated with `name` or `messageRef` in practice — the message reference usually lives on the *connected node* instead. The compiler resolves each flow's display name in this order:

1. The `messageFlow` element's own `name` attribute.
2. The `messageFlow` element's own `messageRef` attribute, resolved against a root-level `<bpmn:message>`.
3. The target node's own `messageRef` — a `sendTask`/`receiveTask`'s `messageRef` attribute, or a boundary event's `messageEventDefinition messageRef`.
4. The same lookup against the source node.

If none of these resolve, `MessageDef.Name` (and `StageDef.BoundaryMessage.MessageName` / `*.MessagePaths[].MessageName` for boundary events) is empty, and `MISSING_MESSAGE_DEFINITION` is emitted as a **warning** — the flow still compiles, but the Execution Service will not be able to correlate that message. Declare a `<bpmn:message>` and reference it via `messageRef` on the flow or the connected node to fix it.

**Ignored pool pattern**: A participant process may carry `<zeebe:property name="ignore" value="true"/>` inside its process-level `<zeebe:properties>`. The compiler skips it entirely — it is not validated, not compiled, and does not appear in the `CompiledCollaboration`'s plans. Use this pattern to model message-flow context for the main pool (e.g. a consultant pool that only sends a trigger message) without bringing that pool's logic into the execution engine.

```xml
<bpmn:process id="Process_consultant" isExecutable="true">
  <bpmn:extensionElements>
    <zeebe:properties>
      <zeebe:property name="ignore" value="true"/>
    </zeebe:properties>
  </bpmn:extensionElements>
  ...
</bpmn:process>
```

**Implicit-start pattern**: A process in a collaboration may have **no explicit start event**. This is valid when the process is started externally (by the execution engine or orchestrator) and its root activity has no incoming sequence flow. The compiler identifies the implicit root via `FindImplicitStart` — the single node with no incoming sequence flow and at least one outgoing sequence flow. This node is exempt from the "no incoming sequence flow" dangling check. A `NO_START_EVENT` error is only raised if no implicit root can be identified.

Typical use: a `callActivity` is the first element in the process; an ignored pool sends a message that arrives at a `messageEventDefinition` boundary event **on** that callActivity, acting as a concurrent notification or interrupt without the main process requiring an explicit start event.

---

## Module BPMNs (Called Processes)

A called process referenced by `<bpmn:callActivity>` must be provided as a separate BPMN file and uploaded via the `module_bpmn_xmls` field on the draft version. The service merges all module BPMNs in-memory before compile and validate — no extra DB round-trips.

**Rules:**

- The main BPMN must contain `<zeebe:calledElement processId="<id>"/>` on every callActivity.
- Each module BPMN must define the corresponding `<bpmn:process id="<id>"/>` with `isExecutable="true"`.
- Module BPMNs may contain their own lanes/departments; these surface alongside the parent's departments in the compiled plan.
- If the module has **no lanes**, the callActivity must include a `<zeebe:ioMapping>` with a `dept_id` input. This determines which department the module's tasks compile into and allows any department to call the same module.
- If the module has its own lanes, the caller does NOT provide `dept_id` — the module uses its own lane names as department IDs.
- Diagrams in module BPMNs are discarded — only process elements are merged.
- Timer and error boundary events attached to a callActivity are not supported and produce a compile error. Message boundary events ARE supported on a callActivity — they compile to `ExecutionStep.MessagePaths` on the flattened step.
- Nested `<bpmn:subProcess>` inside another subProcess is not supported (`NESTED_SUBPROCESS_NOT_SUPPORTED`).
- Modules are versioned and cloned together with the main BPMN.

**Example — calling a no-lane module:**

```xml
<bpmn:callActivity id="CA_PrepResponse" name="Prepare Response">
  <bpmn:extensionElements>
    <zeebe:calledElement processId="Process_PrepareResponse" propagateAllChildVariables="false"/>
    <zeebe:ioMapping>
      <zeebe:input source="engineering" target="dept_id"/>
      <zeebe:input source="=clarificationId" target="clarificationId"/>
    </zeebe:ioMapping>
  </bpmn:extensionElements>
</bpmn:callActivity>
```

`dept_id` is compiler-internal: it assigns the department in the compiled plan but is NOT emitted in the `io_mapping` field of the compiled `ExecutionStep`. Other inputs (like `clarificationId`) ARE emitted and consumed by the Execution Service when creating the child workflow.

**Alternative: `target="Depts"` dict-injection**: When a called process has lanes with generic names (e.g. "Sender", "Receiver") and the same callActivity is used in multiple contexts where the actual departments differ, use `target="Depts"` instead of `dept_id`. The source is a JSON object mapping lane names (case-insensitive) to actual department IDs:

```xml
<zeebe:ioMapping>
  <zeebe:input source='{"sender":"Consultant","receiver":"Tender"}' target="Depts"/>
</zeebe:ioMapping>
```

This also satisfies `MISSING_DEPT_INPUT_FOR_MODULE` for lane-based modules. The `Depts` input is compiler-internal and is stripped from `ExecutionStep.IOMapping` — it is not passed to the Execution Service.

**isExecutable semantics:**

- `isExecutable="false"` → visual-only pool (e.g. a Client pool showing only message flows); skipped entirely by the compiler — not compiled, not usable as a called process.
- `isExecutable="true"` → required for all pools with real business logic, including module/called processes.

The compiler distinguishes root entry-point processes from called processes by whether the process ID appears as a `zeebe:calledElement` target — NOT by the `isExecutable` flag.

---

## Lanes and Departments

```xml
<bpmn:laneSet id="LaneSet_1">
  <bpmn:lane id="Lane_tender" name="Tender">
    <bpmn:flowNodeRef>Task_prep_bnb</bpmn:flowNodeRef>
    <bpmn:flowNodeRef>Task_review_bnb</bpmn:flowNodeRef>
  </bpmn:lane>
  <bpmn:lane id="Lane_engineering" name="Engineering">
    <bpmn:flowNodeRef>Task_ack_engineering</bpmn:flowNodeRef>
  </bpmn:lane>
</bpmn:laneSet>
```

**System semantics**: each lane becomes a `DepartmentDef`. `lane.name` is used as both `DepartmentDef.ID` and `DepartmentDef.Label` — the format is owned by the Profile Service and is trusted as-is. Every `<bpmn:userTask>` must appear in exactly one lane; the compiler derives the department from `<bpmn:flowNodeRef>` membership, not from any Zeebe property.

Additional `DepartmentDef` fields populated by the compiler:

| Field | Type | Description |
| --- | --- | --- |
| `Ignore` | `bool` | `true` when the department's participant pool is an ignored pool (carries `ignore = "true"` on the process-level `<zeebe:properties>`) |
| `Props` | `map[string]string` | Arbitrary key/value pairs from `<zeebe:properties>` on the lane element itself |

Limits: 1–100 lanes per process.

---

## Supported Elements

### Tier 1 — Supported

| Element | System semantics |
|---|---|
| `<bpmn:collaboration>` | Wraps N participants → `CompiledCollaboration` |
| `<bpmn:participant>` | `isExecutable="true"` → `CompiledPlan`; non-executable → compiled as `Ignored: true` plan in `CompiledCollaboration`; execution skipped |
| `<bpmn:messageFlow>` | Cross-participant handoff → `MessageDef` linking plans by message name |
| `<bpmn:message>` | Message definition referenced by start/send/receive elements |
| `<bpmn:process>` | Core container → `CompiledPlan` |
| `<bpmn:laneSet>` / `<bpmn:lane>` | Department grouping → `DepartmentDef` |
| `<bpmn:startEvent>` | Blank, `messageEventDefinition`, `timerEventDefinition`, or `signalEventDefinition` — compiled as trigger metadata; no effect on execution graph |
| `<bpmn:endEvent>` | Blank or named (outcome label); `errorEventDefinition` allowed inside subprocess only |
| `<bpmn:userTask>` | Human work unit → `StageDef` inside its `DepartmentDef`; must carry `zeebe:taskDefinition` + `zeebe:assignmentDefinition`; must be in one lane; optional `zeebe:taskSchedule` for due/follow-up dates |
| `<bpmn:sendTask>` | Outbound message task → `StageDef` with `type: "send_task"`; message name in `extras["message"]` |
| `<bpmn:receiveTask>` | Inbound message wait → `StageDef` with `type: "receive_task"`; message name in `extras["message"]` |
| `<bpmn:parallelGateway>` | Concurrent fan-out/fan-in → parallel `ExecutionStep`; split and join must be matched |
| `<bpmn:exclusiveGateway>` | Routing decision → `ExclusiveBranch` list; all outgoing flows require `conditionExpression`; back-edges allowed when guarded |
| `<bpmn:eventBasedGateway>` | Waits for the first of N intermediate catch events |
| `<bpmn:subProcess>` | Embedded, non-event, non-ad-hoc → `SubWorkflowStep`; recursively validated; nested `subProcess` inside another `subProcess` is **not supported** (`NESTED_SUBPROCESS_NOT_SUPPORTED`) |
| `<bpmn:callActivity>` | References a called process by `<zeebe:calledElement processId="..."/>`; the called process BPMN must be supplied in `module_bpmn_xmls` on the draft; called process uses its own lanes; steps flattened directly into parent plan (no `SubWorkflowStep`); if called process has no lanes, a `dept_id` input mapping is required (see §Module BPMNs) |
| `<bpmn:boundaryEvent cancelActivity="…">` + `timerEventDefinition` | SLA deadline → `StageDef.BoundaryTimer`; attached to `userTask` or `subProcess`; timer boundary events on `callActivity` are rejected at compile time; `cancelActivity="false"` = non-interrupting, `"true"` = interrupting |
| `<bpmn:boundaryEvent>` + `errorEventDefinition` | Exception catch → `SubWorkflowStep.ErrorPaths`; attached to `subProcess` or `callActivity`; error boundaries on `callActivity` pass validation but fail compilation |
| `<bpmn:boundaryEvent cancelActivity="…">` + `messageEventDefinition` | Inbound message interrupt → `StageDef.BoundaryMessage` (userTask), `SubWorkflowStep.MessagePaths` (subProcess), or `ExecutionStep.MessagePaths` (callActivity, flattened); attached to `userTask`, `subProcess`, or `callActivity`; zero outgoing flows is a valid terminal interrupt notification |
| `<bpmn:intermediateCatchEvent>` + `timerEventDefinition` | Wait/delay step |
| `<bpmn:sequenceFlow>` | Structural connector |
| `<bpmn:conditionExpression>` (child of sequenceFlow) | FEEL expression for routing decisions; stored verbatim; evaluated at runtime by the Execution Service; max 4096 chars |
| `<bpmn:error>` | Error definition referenced by error events |
| `<zeebe:taskDefinition>` | Stage type and worker routing — see §User Task Contract |
| `<zeebe:assignmentDefinition>` | Task ownership — see §User Task Contract |
| `<zeebe:properties>` / `<zeebe:property>` | Domain flags — see §User Task Contract |
| `<zeebe:taskSchedule>` | Optional due date / follow-up date — see §User Task Contract |
| `<zeebe:subscription>` | Message correlation key on `receiveTask` |

### Tier 2 — Planned (not yet compiled)

| Element | Notes |
|---|---|
| `<bpmn:inclusiveGateway>` | OR-split/join; parsed and graph-validated but no compile handler — returns `UNSUPPORTED_ELEMENT` |
| `<bpmn:intermediateCatchEvent>` + `messageEventDefinition` | Catch an inbound message mid-process |
| `<bpmn:intermediateCatchEvent>` + `signalEventDefinition` | Catch a broadcast signal mid-process |
| `<bpmn:multiInstanceLoopCharacteristics>` on `userTask` | Panel voting (all-must-complete) |

### Tier 3 — Rejected (parser denylist)

`scriptTask`, `businessRuleTask`, `transaction`, `adHocSubProcess`, `complexGateway`, terminate/compensate/cancel/conditional/link event definitions, `dataObject`, `dataStore`, `standardLoopCharacteristics`

These elements trigger `REJECTED_ELEMENT` on upload.

---

## User Task Contract

Every `<bpmn:userTask>` requires these extension elements:

```xml
<bpmn:userTask id="Task_tender_prep_bnb" name="Prepare Bid-No-Bid">
  <bpmn:extensionElements>

    <!-- Required: stage type — injected by the modeler template, not typed manually -->
    <zeebe:taskDefinition type="prep"/>

    <!-- Required: who can work on this task -->
    <zeebe:assignmentDefinition
      candidateGroups="bd-agent"
      candidateUsers="018e1f2a-0000-7000-8000-000000000001"/>

    <!-- Optional domain flags -->
    <zeebe:properties>
      <zeebe:property name="requires_comment" value="false"/>
    </zeebe:properties>

  </bpmn:extensionElements>
  <bpmn:incoming>Flow_1</bpmn:incoming>
  <bpmn:outgoing>Flow_2</bpmn:outgoing>
</bpmn:userTask>
```

### `zeebe:taskDefinition`

| Attribute | Required | Rules |
|---|---|---|
| `type` | Yes | Must match a registered stage type handler ID. Default set: `prep`, `review`, `approve`. The registry is configured at deployment — the compiler does not hardcode this list. |
| `retries` | No | Silently ignored (Camunda Modeler default). |

**System semantics**: `type` → `StageDef.Type`. The matched `StageTypeHandler.ActivityName()` → `StageDef.Activity` (e.g. `PrepActivity`). The Execution Service uses `Activity` to route the task to the correct job worker.

### `zeebe:assignmentDefinition`

| Attribute | Required | Rules |
|---|---|---|
| `candidateGroups` | Yes | IAM role level name; validated against Membership Service at publish time; max 256 chars |
| `candidateUsers` | Yes | Single UUID v7 (one assignee currently enforced; will be relaxed later) |

**System semantics**: `candidateGroups` → `StageDef.Role` (passed as `?level=<role>` to Membership eligibility check). `candidateUsers` → `StageDef.DefaultAssignees` (pre-assigned when the task starts in Execution Service).

### `zeebe:properties`

Every `<zeebe:property>` element is forwarded verbatim into `StageDef.Extras` (`map[string]string`) — the compiler does not special-case any property name. The Execution Service and job workers own interpretation. For example, `<zeebe:property name="requires_comment" value="true"/>` appears as `extras["requires_comment"] = "true"`, and `<zeebe:property name="sla_category" value="high"/>` appears as `extras["sla_category"] = "high"`.

### `zeebe:taskSchedule`

Optional scheduling hints for a user task:

```xml
<zeebe:taskSchedule dueDate="2025-12-31T17:00:00Z" followUpDate="2025-12-30T09:00:00Z" />
```

| Attribute | Required | Rules |
|---|---|---|
| `dueDate` | No | FEEL expression or ISO 8601 datetime; stored verbatim; evaluated by the Execution Service |
| `followUpDate` | No | FEEL expression or ISO 8601 datetime; stored verbatim; evaluated by the Execution Service |

**System semantics**: extracted to `StageDef.DueDate` and `StageDef.FollowUpDate`. Both fields are absent from the JSON when the element is omitted. The compiler does not validate or interpret the values — they are opaque strings passed through to the Execution Service.

### Compiled output fields

Every compiled task produces a `StageDef` with:

- `node_id` — the BPMN element `id` attribute (e.g. `"Activity_0abc123"`). Use this for stable machine routing (resume-after-pause, transition targeting) in preference to name-based lookups.
- `extras` — unknown zeebe:properties plus, for `send_task`/`receive_task`, the resolved message name under `extras["message"]`.

Every `ExclusiveBranch` carries:

- `target_node_id` — BPMN element ID of the forward target (machine routing).
- `revert_to_node_id` — BPMN element ID of the revert target on guarded-loop back-edges.

### sendTask / receiveTask extension

Send and receive tasks compile to `StageDef` with type `"send_task"` or `"receive_task"`. Extension elements are optional:

```xml
<bpmn:sendTask id="Task_issue_rfq" name="Issue RFQ" messageRef="Msg_rfq">
  <bpmn:extensionElements>
    <!-- Optional: role and default assignee, same as userTask -->
    <zeebe:assignmentDefinition candidateGroups="tender-business" candidateUsers="018e1f2a-0000-7000-8000-000000000020"/>
  </bpmn:extensionElements>
</bpmn:sendTask>

<bpmn:receiveTask id="Task_receive_ack" name="Receive Ack" messageRef="Msg_ack">
  <bpmn:extensionElements>
    <!-- Optional: same assignment contract as above -->
    <zeebe:assignmentDefinition candidateGroups="tender-business" candidateUsers="018e1f2a-0000-7000-8000-000000000020"/>
    <!-- zeebe:subscription is passed through unchanged; Execution Service uses it for correlation -->
    <zeebe:subscription messageCorrelationKey="tenderId"/>
  </bpmn:extensionElements>
</bpmn:receiveTask>
```

The message name is resolved from the `messageRef` attribute to a `<bpmn:message>` definition and placed in `extras["message"]` of the compiled `StageDef`. `zeebe:taskDefinition` is not required on send/receive tasks.

---

## Routing vs Rejection Semantics

### Use XOR + `conditionExpression` for routing decisions

An exclusive gateway routes the process to one of several paths based on an explicit decision — set by a user or derived from process data.

```xml
<bpmn:exclusiveGateway id="XOR_bid_decision"/>

<bpmn:sequenceFlow id="Flow_nobid" sourceRef="XOR_bid_decision" targetRef="End_nobid">
  <bpmn:conditionExpression>= decision = "no-bid"</bpmn:conditionExpression>
</bpmn:sequenceFlow>

<bpmn:sequenceFlow id="Flow_bid" sourceRef="XOR_bid_decision" targetRef="Task_organise_team">
  <bpmn:conditionExpression>= decision = "bid"</bpmn:conditionExpression>
</bpmn:sequenceFlow>
```

Condition expressions use FEEL syntax (prefix `= ` for expression mode). They are stored verbatim in the compiled plan; the Execution Service evaluates them at runtime.

### Use error events for stage rejection / rework

When an approval stage rejects, the work must loop back. Model this with a subprocess containing the rework-candidate stages and an error boundary event that catches rejection.

```xml
<bpmn:subProcess id="SP_strategy_approval" name="Strategy Approval">
  <bpmn:startEvent id="Start_sp"/>
  <bpmn:userTask id="Task_prep_strategy" name="Prepare Strategy">
    <!-- zeebe:taskDefinition type="prep" ... -->
  </bpmn:userTask>
  <bpmn:userTask id="Task_approve_strategy" name="Approve Strategy">
    <!-- zeebe:taskDefinition type="approve" ... -->
  </bpmn:userTask>
  <!-- Worker signals rejection by completing with errorCode="strategy-rejected" -->
  <bpmn:endEvent id="End_rejected">
    <bpmn:errorEventDefinition errorRef="Err_strategy_rejected"/>
  </bpmn:endEvent>
  <!-- ...sequence flows... -->
</bpmn:subProcess>

<bpmn:boundaryEvent id="Boundary_rejected" attachedToRef="SP_strategy_approval"
                    cancelActivity="true">
  <bpmn:errorEventDefinition errorRef="Err_strategy_rejected"/>
</bpmn:boundaryEvent>
<!-- Boundary → route to rework or end -->

<bpmn:error id="Err_strategy_rejected" errorCode="strategy-rejected"/>
```

**System semantics**: error end events inside a subprocess compile to `SubWorkflowStep.ErrorPaths`. The Execution Service matches `errorCode` on task completion, cancels the subprocess (interrupting boundary), and follows the error boundary's outgoing flow.

---

## Timer Boundary Events (SLA)

Attach to a `userTask` to enforce a deadline:

```xml
<!-- Non-interrupting: escalation runs concurrently; original task continues -->
<bpmn:boundaryEvent id="Timer_strategy_sla" attachedToRef="Task_prep_strategy"
                    cancelActivity="false">
  <bpmn:timerEventDefinition>
    <bpmn:timeDuration>PT48H</bpmn:timeDuration>
  </bpmn:timerEventDefinition>
</bpmn:boundaryEvent>

<!-- Interrupting: task is cancelled; only escalation path proceeds -->
<bpmn:boundaryEvent id="Timer_hard_deadline" attachedToRef="Task_approve_offer"
                    cancelActivity="true">
  <bpmn:timerEventDefinition>
    <bpmn:timeDuration>P7D</bpmn:timeDuration>
  </bpmn:timerEventDefinition>
</bpmn:boundaryEvent>
```

Duration format: ISO 8601 (`PT48H`, `P3D`, `PT2H30M`, `P3W`) or Go duration (`48h`). Both forms are accepted and stored verbatim — the compiler does not normalise the value.

**System semantics**: compiles to `StageDef.BoundaryTimer {Duration, Interrupting}`. On fire, the Execution Service either creates a parallel escalation task (non-interrupting) or cancels the current task and starts the escalation path (interrupting). Max one timer boundary per task.

---

## Message Boundary Events

A `<bpmn:boundaryEvent>` with a `messageEventDefinition` can be attached to a `userTask`, `subProcess`, or `callActivity` to catch an inbound message while the host activity is active.

**Terminal interrupt (no outgoing flow)**: A message boundary event with **zero outgoing sequence flows** is valid — it is a terminal interrupt notification. The message fires, the host activity is cancelled (interrupting) or left running (non-interrupting), and there is no continuation path. This is the idiomatic pattern when an ignored pool sends a message that simply terminates or signals the host activity without routing to further steps.

```xml
<bpmn:boundaryEvent id="Boundary_msg_cancel" attachedToRef="CA_MainWork"
                    cancelActivity="true">
  <bpmn:messageEventDefinition messageRef="Msg_cancel"/>
</bpmn:boundaryEvent>
<!-- No outgoing sequence flow — terminal interrupt -->
```

**Contrast with timer and error boundaries**: Timer and error boundary events still require at least one outgoing sequence flow; a timer or error boundary with no continuation is a modelling error and will fail validation.

**System semantics**: compiles to `StageDef.BoundaryMessage {message_name, interrupting, target_dept}` when attached to a `userTask`, to `SubWorkflowStep.MessagePaths` when attached to a `subProcess`, or to `ExecutionStep.MessagePaths` when attached to a `callActivity` (flattened into the parent plan). The message name is resolved from the boundary event's own `messageEventDefinition messageRef`, falling back to any collaboration `messageFlow` targeting the boundary event — see §Message Flow Name Resolution.

---

## Gateway Rules

### Parallel gateway

Split (1-in, N-out) and join (N-in, 1-out) must be paired at the same nesting depth. Every split branch must converge at its matching join. Compile to `ExecutionStep.Parallel` (all branches execute concurrently; join waits for all).

### Exclusive gateway

All outgoing flows from a split must carry a `<bpmn:conditionExpression>`. Back-edges (rework loops) are allowed only when the originating XOR split has at least one forward-exiting branch (guarded exit). Compiler validates via DFS + Tarjan SCC. Unguarded cycles → `CYCLE_DETECTED` / `UNGUARDED_LOOP`.

### Inclusive gateway

One or more branches fire based on condition evaluation. Join waits for all active branches.

### Multiple end events

A process may have more than one `<bpmn:endEvent>`. Use the `name` attribute to label outcomes:

```xml
<bpmn:endEvent id="End_nobid" name="No-Bid Closed"/>
<bpmn:endEvent id="End_bid"   name="Bid Committed"/>
```

The Execution Service records the reached end event's name as the workflow outcome.

---

## Validation Limits

| Constraint | Limit | Error code |
|---|---|---|
| Upload size | 10 MB | `PAYLOAD_TOO_LARGE` |
| XML token stream | 1,000,000 tokens | parse error → 400 |
| User tasks per process | 1000 | `TASK_LIMIT_EXCEEDED` |
| Lanes per process | 100 | `LANE_LIMIT_EXCEEDED` |
| Longest forward path | 2000 nodes | `MAX_DEPTH_EXCEEDED` |
| `conditionExpression` length | 4096 chars | `INVALID_CONDITION_EXPRESSION` |
| `candidateGroups` length | 256 chars | `CANDIDATE_GROUPS_EMPTY` |
| `candidateUsers` count | 1 (current) | `INVALID_CANDIDATE_USER` |

---

## BPMN Error Code Reference

| Code | Trigger |
|---|---|
| `MISSING_NAMESPACE` | `bpmn` or `zeebe` namespace absent on `<bpmn:definitions>` |
| `REJECTED_ELEMENT` | Element on the parser denylist (Tier 3) |
| `MISSING_TASK_DEFINITION` | `zeebe:taskDefinition` absent on a `userTask` |
| `MISSING_ASSIGNMENT_DEFINITION` | `zeebe:assignmentDefinition` absent on a `userTask` |
| `INVALID_TASK_DEFINITION_TYPE` | *(deprecated — superseded by `UNKNOWN_STAGE_TYPE` warning)* `type` not in the registered `StageTypeHandler` set |
| `UNKNOWN_STAGE_TYPE` | *(warning severity)* `type` attribute value not in the registered `StageTypeHandler` set; compilation continues |
| `TASK_NOT_IN_LANE` | `userTask` not referenced by any `<bpmn:flowNodeRef>` |
| `CANDIDATE_GROUPS_EMPTY` | `candidateGroups` absent or exceeds 256 chars |
| `INVALID_CANDIDATE_USER` | `candidateUsers` is not a valid UUID v7 |
| `INVALID_CONDITION_EXPRESSION` | Condition expression exceeds 4096 chars |
| `INVALID_ZEEBE_PROPERTY` | *(warning severity)* module `zeebe:ioMapping` `target="Depts"` input source is not valid JSON / not a `map[string]string` |
| `INVALID_SLA_DURATION` | Timer boundary duration is not valid ISO 8601 or Go duration |
| `MISSING_MESSAGE_DEFINITION` | `messageRef` on a flow or task references an undeclared `<bpmn:message>` |
| `UNMATCHED_MESSAGE_FLOW` | Message flow source/target does not reference a known element |
| `TASK_LIMIT_EXCEEDED` | User task count > 1000 |
| `LANE_LIMIT_EXCEEDED` | Lane count > 100 |
| `MULTIPLE_START_EVENTS` | More than one `startEvent` in the process |
| `MULTIPLE_END_EVENTS` | More than one `endEvent` (only raised when collaboration is absent and single process has > 1 end events without guarded exits) |
| `NO_START_EVENT` | Process has no `startEvent` |
| `NO_END_EVENT` | Process has no `endEvent` |
| `MULTIPLE_PROCESSES` | More than one `<bpmn:process>` without a wrapping collaboration |
| `DANGLING_NODE` | Node has no incoming or outgoing sequence flow |
| `UNREACHABLE_NODE` | Node not reachable from `startEvent` via DFS |
| `INVALID_SEQUENCE_FLOW_REF` | `sequenceFlow` references unknown `sourceRef` or `targetRef` |
| `UNMATCHED_GATEWAY` | Split gateway has no reachable matching join |
| `CYCLE_DETECTED` | Tarjan SCC found a strongly-connected component without a guarded exit |
| `UNGUARDED_LOOP` | Back-edge originates at a gateway with no forward exit |
| `MAX_DEPTH_EXCEEDED` | Longest forward path exceeds 2000 nodes |
| `INVALID_BOUNDARY_ATTACHMENT` | Boundary event attached to an unsupported element type |
| `MISSING_DIAGRAM` | No `<bpmndi:BPMNDiagram>` found in the BPMN document |
| `MISSING_DIAGRAM_SHAPE` | A BPMN element has no corresponding `<bpmndi:BPMNShape>` in the diagram |
| `UNRESOLVED_CALLED_ELEMENT` | `callActivity` references a `processId` not found in supplied module BPMNs |
| `NESTED_SUBPROCESS_NOT_SUPPORTED` | A `subProcess` is nested inside another `subProcess` |
| `MISSING_DEPT_INPUT_FOR_MODULE` | Called process has no lanes and no `dept_id` input mapping was provided |
| `UNSUPPORTED_ELEMENT` | Tier 2 element present (e.g. `inclusiveGateway`) — parsed but compile handler absent |
