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

Limits: 1–100 lanes per process.

---

## Supported Elements

### Tier 1 — Supported

| Element | System semantics |
|---|---|
| `<bpmn:collaboration>` | Wraps N participants → `CompiledCollaboration` |
| `<bpmn:participant>` | `isExecutable="true"` → `CompiledPlan`; non-executable → structural context only |
| `<bpmn:messageFlow>` | Cross-participant handoff → `MessageDef` linking plans by message name |
| `<bpmn:message>` | Message definition referenced by start/send/receive elements |
| `<bpmn:process>` | Core container → `CompiledPlan` |
| `<bpmn:laneSet>` / `<bpmn:lane>` | Department grouping → `DepartmentDef` |
| `<bpmn:startEvent>` | Blank, `messageEventDefinition`, `timerEventDefinition`, or `signalEventDefinition` — compiled as trigger metadata; no effect on execution graph |
| `<bpmn:endEvent>` | Blank or named (outcome label); `errorEventDefinition` allowed inside subprocess only |
| `<bpmn:userTask>` | Human work unit → `StageDef` inside its `DepartmentDef`; must carry `zeebe:taskDefinition` + `zeebe:assignmentDefinition`; must be in one lane |
| `<bpmn:sendTask>` | Cross-participant message sender → `MessageStep(direction=send)` |
| `<bpmn:receiveTask>` | Waits for named message → `MessageStep(direction=receive)` |
| `<bpmn:parallelGateway>` | Concurrent fan-out/fan-in → parallel `ExecutionStep`; split and join must be matched |
| `<bpmn:exclusiveGateway>` | Routing decision → `ExclusiveBranch` list; all outgoing flows require `conditionExpression`; back-edges allowed when guarded |
| `<bpmn:inclusiveGateway>` | OR-split/join → one or more branches fire based on conditions |
| `<bpmn:eventBasedGateway>` | Waits for the first of N intermediate catch events |
| `<bpmn:subProcess>` | Embedded, non-event, non-ad-hoc → `SubWorkflowStep`; recursively validated |
| `<bpmn:boundaryEvent cancelActivity="…">` + `timerEventDefinition` | SLA deadline → `StageDef.BoundaryTimer`; `cancelActivity="false"` = non-interrupting, `"true"` = interrupting |
| `<bpmn:boundaryEvent>` + `errorEventDefinition` | Exception catch on subprocess → `SubWorkflowStep.ErrorPaths` |
| `<bpmn:intermediateCatchEvent>` + `timerEventDefinition` | Wait/delay step |
| `<bpmn:sequenceFlow>` | Structural connector |
| `<bpmn:conditionExpression>` (child of sequenceFlow) | FEEL expression for routing decisions; stored verbatim; evaluated at runtime by the Execution Service; max 4096 chars |
| `<bpmn:error>` | Error definition referenced by error events |
| `<zeebe:taskDefinition>` | Stage type and worker routing — see §User Task Contract |
| `<zeebe:assignmentDefinition>` | Task ownership — see §User Task Contract |
| `<zeebe:properties>` / `<zeebe:property>` | Domain flags — see §User Task Contract |
| `<zeebe:subscription>` | Message correlation key on `receiveTask` |

### Tier 2 — Planned (not yet compiled)

| Element | Notes |
|---|---|
| `<bpmn:intermediateCatchEvent>` + `messageEventDefinition` | Catch an inbound message mid-process |
| `<bpmn:intermediateCatchEvent>` + `signalEventDefinition` | Catch a broadcast signal mid-process |

### Tier 3 — Rejected (parser denylist)

`scriptTask`, `businessRuleTask`, `transaction`, `adHocSubProcess`, `complexGateway`, terminate/compensate/cancel/conditional/link event definitions, `callActivity`, `dataObject`, `dataStore`, `standardLoopCharacteristics`

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

| Property name | Type | Required | Default |
|---|---|---|---|
| `requires_comment` | `"true"` / `"false"` | No | `false` |

**System semantics**: `requires_comment` → `StageDef.RequiresComment`. When `true`, the Execution Service enforces a non-empty comment before the task can be completed.

### sendTask extension

```xml
<bpmn:sendTask id="Task_issue_rfq" name="Issue RFQ to Contractor">
  <bpmn:extensionElements>
    <zeebe:taskDefinition type="send.rfq"/>
  </bpmn:extensionElements>
</bpmn:sendTask>
```

`type` identifies the message worker in the Execution Service.

### receiveTask extension

```xml
<bpmn:receiveTask id="Task_receive_rfq" name="Receive RFQ" messageRef="Msg_rfq">
  <bpmn:extensionElements>
    <zeebe:subscription messageCorrelationKey="tenderId"/>
  </bpmn:extensionElements>
</bpmn:receiveTask>
```

`messageCorrelationKey` is the variable name used by the Execution Service to match the incoming message to the correct workflow instance.

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

Duration format: ISO 8601 (`PT48H`, `P3D`, `PT2H30M`) or Go duration (`48h`). The UI sends ISO 8601; the compiler normalises to Go duration internally.

**System semantics**: compiles to `StageDef.BoundaryTimer {Duration, Interrupting}`. On fire, the Execution Service either creates a parallel escalation task (non-interrupting) or cancels the current task and starts the escalation path (interrupting). Max one timer boundary per task.

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
| `INVALID_TASK_DEFINITION_TYPE` | `type` not in the registered `StageTypeHandler` set |
| `TASK_NOT_IN_LANE` | `userTask` not referenced by any `<bpmn:flowNodeRef>` |
| `CANDIDATE_GROUPS_EMPTY` | `candidateGroups` absent or exceeds 256 chars |
| `INVALID_CANDIDATE_USER` | `candidateUsers` is not a valid UUID v7 |
| `INVALID_CONDITION_EXPRESSION` | Condition expression exceeds 4096 chars |
| `INVALID_ZEEBE_PROPERTY` | `requires_comment` is not a parseable boolean |
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
