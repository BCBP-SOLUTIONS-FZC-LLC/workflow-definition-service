# BPMN Designer Guide

This guide is for business analysts and process owners who model workflows in Camunda Modeler. You do not write code or configure system properties — those are handled by IAM templates and the enrichment team. Your job is to draw the correct process structure.

---

## What the system does with your diagram

When a BPMN file is uploaded, the system:

1. Validates it against the structural rules in this guide.
2. Compiles it into an execution plan used by the Workflow Engine.
3. Rejects it with a list of errors if any rule is broken.

Your diagram must pass all checks before it can go live.

---

## Lanes are departments

**Every task must sit inside a lane.** Lanes map directly to departments in the compiled plan and the UI. The lane name becomes the department identifier — it is used for routing, display, and access control.

Name lanes after real organisational departments. Do not rename a lane after a workflow has gone live without coordinating with the engineering and IAM teams.

```
┌──────────────────────────────────┐
│ Pool: Bid-No-Bid Review          │
├─────────────────┬────────────────┤
│ Tender Business │  Engineering   │
│   (lane)        │  (lane)        │
│  ┌──────────┐   │  ┌──────────┐  │
│  │  Task A  │   │  │  Task B  │  │
│  └──────────┘   │  └──────────┘  │
└─────────────────┴────────────────┘
```

---

## Task types you can use

### User Task (most common)

A task that appears in someone's work queue. Use an IAM-provided Camunda Modeler element template to stamp it — this fills in the stage type and assignment fields automatically. Do not type these values by hand.

The lane the task sits in determines which department owns it. The task name is what users see in the UI.

- You can attach a timer boundary event for SLA escalation, or a message boundary event to interrupt the task when an inbound message arrives.

### Send Task

Use when the workflow sends a message or notification to an external participant (e.g. issuing an RFQ to a contractor). Connect it to a `<bpmn:message>` definition.

- The task name describes what is being sent.
- Assignment is optional and filled by the enrichment team.

### Receive Task

Use when the workflow waits for a response from an external participant (e.g. waiting for contractor documents). Connect it to a `<bpmn:message>` definition and set a correlation key so the Execution Service can match the incoming response.

- The task name describes what is being waited for.
- Assignment is optional and filled by the enrichment team.

### Sub-Process

Groups a sequence of tasks into a named block (e.g. "Prepare Strategy"). It appears as a single named step in the Execution Service; its internal tasks are compiled separately.

- Sub-processes must have their own start and end events.
- Tasks inside inherit the parent lane when they have no lane of their own.
- You can attach a timer boundary event for SLA escalation.
- You can attach an error boundary event to catch failures and re-route.
- You can attach a message boundary event to interrupt the sub-process when an inbound message arrives.

### Called Process (callActivity)

Use when you want to invoke a reusable process that is defined in its own BPMN file. The definition team uploads the module BPMN separately via `module_bpmn_xmls` on the draft; the compiler merges them in-memory before validation.

- Stamp the element with `<zeebe:calledElement processId="…"/>` pointing to the module's process ID (fill this via the element template — do not type it by hand).
- The called process BPMN must have `isExecutable="true"`.
- If the module has **no lanes of its own**, add a `<zeebe:ioMapping>` with a `dept_id` input identifying which department owns the steps (e.g. `dept_id = engineering`). This tells the compiler which department to compile those tasks into.
- If the module has its own lanes, no `dept_id` is needed — the module's lanes define the departments.
- **Timer and error boundary events on a callActivity are not supported** — rejected at compile time. Use a sub-process if you need timer/error boundary semantics on a called process. **Message boundary events ARE supported directly on a callActivity** — they compile to the flattened step's `message_paths`.
- The called process steps are compiled flat into the parent plan — they are not wrapped in a sub-workflow block.

---

## Gateways

### Exclusive Gateway (XOR) — one path fires

Routes to exactly one downstream path. Two uses:

**1. Multi-way forward routing** (different outcomes lead to different paths)

Each outgoing flow needs a condition expression. See [Condition Expressions](#condition-expressions).

**2. Approve / reject loop** (a task can be sent back for rework)

The back-edge (revert path) returns to a previous task. No condition expressions are needed — the Execution Service routes based on whether the user approved or rejected the task. The gateway must always have at least one forward exit even when a rejection path exists.

### Parallel Gateway (AND) — all paths fire

Splits the flow into simultaneous branches. All branches must converge at a matching AND join. Use when multiple departments work at the same time.

### Inclusive Gateway (OR) — one or more paths fire

Like parallel, but only branches whose conditions are true fire. Prefer XOR or AND unless you specifically need conditional fan-out.

---

## Condition Expressions

Conditions are placed on sequence flows leaving an exclusive gateway. They tell the Execution Service which path to follow at runtime.

### When you need them

**Multi-way forward routing** — when different task outcomes lead to genuinely different destinations:

```
               ┌──(decision = "no-bid")──→ End (no-bid)
Decision XOR ──┤
               └──(decision = "bid")────→ Organise Team
```

Each outgoing flow must have a condition. The variable names (like `decision`) are agreed with engineering — they are set at runtime by the task worker.

### When you do NOT need them

**Approve / reject loops** — the user either continues or reverts to a previous task:

```
Prep → XOR ──(approve: forward)──→ Review
        │
        └──(reject: revert)──────→ Prep
```

No condition expressions needed. The Execution Service reads the task signal directly:

- User clicks **Continue / Approve** → forward branch fires.
- User clicks **Reject / Revert** → back-edge fires.

The compiled plan already knows the target of each branch from the BPMN element IDs — conditions are not evaluated for approve/reject decisions.

### Format

Conditions use FEEL (Friendly Enough Expression Language). Agree the variable names with engineering before modelling.

| Pattern | Example |
|---|---|
| String equality | `= decision = "no-bid"` |
| Numeric comparison | `= score < 70` |
| Boolean flag | `= is_complete = true` |

Conditions are stored verbatim and evaluated at runtime. The definition service does not validate their logic — only that they are present on the right flows.

---

## Structural rules

These are checked on every upload. Breaking them causes rejection.

| Rule | Error |
|---|---|
| Every task inside a lane | `TASK_NOT_IN_LANE` |
| Exactly one start event per process | `NO_START_EVENT` / `MULTIPLE_START_EVENTS` |
| At least one end event per process | `NO_END_EVENT` |
| Every node connected (no floating elements) | `DANGLING_NODE` |
| Every node reachable from start | `UNREACHABLE_NODE` |
| Parallel/inclusive split has a matching join | `UNMATCHED_GATEWAY` |
| Revert loops always have a forward exit (guarded) | `UNGUARDED_LOOP` |
| Sub-processes have their own start and end events | compile error |
| Diagram block present (Camunda Modeler adds this) | `MISSING_DIAGRAM` |

---

## Elements you cannot use

The following are blocked and cause `REJECTED_ELEMENT` on upload:

| Blocked element | Use instead |
|---|---|
| `scriptTask`, `businessRuleTask` | `userTask` with the IAM template |
| `transaction`, `adHocSubProcess`, `complexGateway` | (not supported) |
| Compensate / cancel / conditional / link events | (not supported) |
| `dataObject`, `dataStore` as executable steps | visual annotations only |
| Loop markers (`standardLoopCharacteristics`) | explicit XOR loop |

---

## Common patterns

### Sequential approval chain

```
Start → Prep → Review → Approve → End
```

All tasks in the same lane, straight sequence flows, no gateways.

### Approval with rework (revert loop)

```
Start → Prep → XOR → Review → End
                │
                └──(reject)──→ Prep
```

XOR has two exits: forward and back. No condition expressions needed.

### Multi-department parallel work

```
Start → ┌─ Task A (Tender) ─┐
        ├─ Task B (Eng)     ─┤→ End
        └─ Task C (Legal)  ─┘
```

Use parallel (AND) split and join.

### Multi-way decision

```
Start → Decision Task → XOR ──(bid)────→ Organise Team → ...
                          └──(no-bid)──→ End (no-bid)
```

Both outgoing flows need condition expressions.

### Sub-process with escalation

```
Start → [Sub-Process: Prepare Strategy] → End
                  │
                  └─(timer 48h)──→ Escalation Task → End
```

Attach a timer boundary event to the sub-process.

---

## Before you upload

- [ ] Every task is inside a lane
- [ ] Every task has been stamped with the IAM element template (stage type + assignment)
- [ ] Send/receive tasks reference a `<bpmn:message>` definition
- [ ] XOR gateways with multiple forward paths have condition expressions on every outgoing flow
- [ ] Revert loops always have a forward exit
- [ ] Sub-processes have start and end events inside them
- [ ] No disallowed elements (script task, business rule task, etc.)
- [ ] If you used a called process (callActivity), all module BPMNs are uploaded in the same draft
