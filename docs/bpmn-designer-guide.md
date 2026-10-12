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

## Lanes say who does the work

**Every task must sit inside a lane.** A lane stands for a department: its `dept_id` is the IAM department whose people do the lane's tasks, and its name is what the UI shows.

**The arrows decide the order.** Tasks run in the order the sequence flows reach them, whatever lane they sit in. A lane only says which department does a task, so work can move from one lane to another and back as often as the process needs.

A lane name cannot contain `/`, `::` or `~`.

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

## One pool runs: outside parties are drawn, not run

A file may hold several pools, for example your organisation and a contractor. **Exactly one pool runs: the workflow.** It is the process that is `isExecutable="true"`, is not called by another process, and is not marked ignore. Every other pool stands for an outside party. It is drawn so the picture is complete, and it produces nothing: its tasks are not checked and never appear in anyone's queue.

- Two pools that would both run are `MULTIPLE_PROCESSES`. Mark the outside party's pool ignore, or leave it non-executable. No pool that would run is `NO_RUNNABLE_PROCESS`.
- To mark a pool, or one lane inside the workflow's pool, as an outside party, give it the property `ignore` = `true` (in Camunda Modeler: the pool's or lane's properties panel, a Zeebe property named `ignore`). Do not mark a process ignore if another process calls it: that is `UNRESOLVED_CALLED_ELEMENT`.
- **An ignored lane** is an outside party inside the process that runs. Its user, send and receive tasks pass through: the flow continues past them without waiting. They cannot hold a call, a sub-process or a connector task, nor carry a boundary event, and a loop that only passes through them is not a loop the engine can run. Each of those is `IGNORED_LANE_WORK`.
- When the workflow must wait for an outside party, put a receive task (or a message boundary event) in a lane that runs, and draw the party's message flow into it. The party's answer is delivered through the service that deals with it, for that step (see [Receive Task](#receive-task)).

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
- A receive task that no send task of the workflow feeds, such as one waiting for a message flow from an outside party's pool, is completed by that party's answer. The published version lists it among its `waits`, and the instance continues when the answer is delivered for that step. A message boundary event on a task, sub-process or call is listed the same way.

### Connector Task

A service task whose task definition type is `connector:<name>` runs an automated connector (storage, send-email, rest-call, and so on).

- An input's `source` is `=name` to read the instance variable `name`, or any other text for a literal value. An output's `source` names a key of the connector's result, and its `target` is the instance variable it becomes.
- Never add an input for a credential (`accessKey`, `apiKey`, and the like). The tenant's stored credential is always used, and a diagram that names one is refused (`CONNECTOR_SECRET_AUTHORED`).

### Sub-Process

Groups a sequence of tasks into a named block (e.g. "Prepare Strategy"). It appears as a single named step in the Execution Service; its internal tasks are compiled separately.

- Sub-processes must have their own start and end events.
- Tasks inside inherit the parent lane when they have no lane of their own.
- You can attach a timer boundary event for SLA escalation.
- You can attach an error boundary event to catch failures and re-route.
- You can attach a message boundary event to interrupt the sub-process when an inbound message arrives.

### Called Process (callActivity)

Use when you want to reuse a process instead of drawing it again. The process you call is either:

- **a module from the library**: a process published on its own, by your organisation or by the platform for every organisation. This is the usual case; or
- **a process in the same file**: private to this workflow and saved with it.

**Choosing the module and its version**

- Stamp the element with `<zeebe:calledElement processId="…"/>` naming the module's process ID (pick it from the module library; the ID is the module's `process_id`). It must be a fixed name, not an expression.
- To use one particular published version, add `bindingType="versionTag" versionTag="3"`, where `3` is the module's version number. A pinned version always comes from the library, so it cannot share its ID with a process in this file; validation reports that on the call. Without it, the workflow takes the module's newest published version **at the moment the workflow is published**.
- A published workflow keeps the module versions it was published with. A newer module version, or the module being retired, changes nothing in it or in instances already running; publish the workflow again to pick the change up. The module's usages view shows which workflows are on an older version.
- A call that names a module that doesn't exist, isn't published, or has been retired is reported on the call when you validate.

**Who does the module's work**

- Each lane in the module is a place work happens. A lane with a `dept_id` property always runs in that department. A lane without one is **open**, and each call decides which of its own lanes does that work.
- If the module has one open lane, or no lanes at all, its work runs in the lane the call sits in. Nothing to fill in.
- If it has two or more open lanes, add a `Depts` input mapping each open lane to one of your lanes, for example `{"Sender":"Consultant","Receiver":"Tender"}`. Lanes are matched by name, ignoring case.
- A lane with a `dept_id` cannot be remapped.
- **Every task needs a default person before the workflow can be published.** A module may leave its tasks' people to you. Name them with an `Assignees` input on the call, for example `{"PR_Review":"<user id>"}`, for this call only. For a task in a module that the module itself calls, give the path of calls to it: `{"PR_Check::Check_Task":"<user id>"}`. Your entry wins over one the module sets. A user id is the person's IAM id. Validation tells you which task still has nobody, on the call that runs it. People can still be changed when an instance starts.

**Other rules**

- The called process does not need `isExecutable="true"`; a called process always runs as a module. It must not be marked ignore (see [One pool runs](#one-pool-runs-outside-parties-are-drawn-not-run)).
- You can attach timer, error and message boundary events to a callActivity, with the same meaning as on a sub-process. A boundary's path must start with a task, or go straight to an end event; to call a module or send a message from it, put a task first.
- Each call runs as its own block of work. Calling the same module twice creates two separate sets of tasks, and each one can be assigned on its own.

---

## Boundary events: timers, messages and errors

A boundary event sits on the edge of a user task, a sub-process or a called process. It watches for something while the host is busy.

| Kind | Fires when | Notes |
|---|---|---|
| Timer | the duration has passed (positive ISO 8601, for example `PT48H`, `P5D`) | SLA escalation |
| Message | the named message arrives | the message name is agreed with engineering |
| Error | a stage inside the sub-process or called process fails | sub-process or called process only; always interrupting |

- **Interrupting** (the default): the host stops, its open tasks are closed, and the boundary's path runs in its place. Nothing the host would have gone on to runs; when the path reaches an end event, that route of the process ends there. **Non-interrupting** (`cancelActivity="false"`): the path runs beside the host, which carries on.
- **The path** may go through several tasks and gateways before it ends. It must start with a task or go straight to an end event; a sub-process may start it only when its own first step is a user task (`UNSUPPORTED_BOUNDARY_TARGET` otherwise). To call a module or send a message from it, put a task first.
- **Error boundaries** match by error code. A boundary with an error code catches that failure; one with none catches any failure that no other boundary of that host names. A failure nothing catches fails the instance.
- A boundary on a task in an ignored lane is `IGNORED_LANE_WORK`.

---

## Gateways

### Exclusive Gateway (XOR) — one path fires

Routes to exactly one downstream path. Two uses:

**1. Multi-way forward routing** (different outcomes lead to different paths)

Each outgoing flow needs a condition expression, except one default flow that is taken when no condition holds. See [Condition Expressions](#condition-expressions).

**2. Approve / reject loop** (a task can be sent back for rework)

The back-edge (revert path) returns to a previous task. No condition expressions are needed — the Execution Service routes based on whether the user approved or rejected the task. The gateway must always have at least one forward exit even when a rejection path exists.

The loop can run as many times as the reviewer keeps rejecting — every rejection sends the work back again, and the gateway is re-checked each time the reworked task is completed. There is one safety limit: after **50** trips around the same loop the instance stops with an error rather than continuing. A real process never comes close, so hitting it means something is wrong with the workflow and an operator needs to look. Draw the loop as you want it to behave; there is nothing to configure.

### Parallel Gateway (AND) — all paths fire

Splits the flow into simultaneous branches. All branches must converge at a matching AND join. Use when multiple departments work at the same time.

No branch may leave the fork early. To let a reviewer inside a branch reject the whole item, record the decision in that branch and put the exclusive gateway that ends the process after the AND join.

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

Each outgoing flow must have a condition, except one default flow: the Execution Service takes the first flow without a condition when no condition holds, so a second flow without one can never run (`GATEWAY_BRANCH_WITHOUT_CONDITION`, a warning). The variable names (like `decision`) are agreed with engineering — they are set at runtime by the task worker.

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

## What the validator checks and what the compiler does

The compiler turns each element into plan steps in a fixed way, so the validator refuses a diagram the compiler would turn into something other than what you drew. Draw to these rules and the plan matches the diagram:

- **One way out.** A start event, task, send or receive task, sub-process and call continue along one flow, and the compiler follows only that flow. To run branches at the same time or to choose between them, put a parallel, exclusive or inclusive gateway after the element.
- **Boundaries sit on work that waits.** A timer or message boundary attaches to a user task, a sub-process or a call, and an error boundary to a sub-process or a call. A task has at most one timer and one message boundary, and a boundary's path is one flow.
- **A default flow.** At an exclusive gateway the Execution Service evaluates the conditions in order and takes the first flow whose condition holds, or else the first flow with no condition. Give every flow but one a condition, or the others can never run.
- **A revert returns to earlier work.** A revert flow starts at an exclusive gateway and goes back to a task or a receive task; the compiler does not follow a flow that leaves a receive task backwards.
- **An end event ends the path.** Nothing is drawn after it.
- **Every element has its own id**, and every element is in a lane.

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
| Parallel/inclusive split has a matching join; an exclusive split has one unless every branch ends | `UNMATCHED_GATEWAY` |
| Lane names have no `/`, `::` or `~` | `INVALID_LANE_NAME` |
| Revert loops always have a forward exit (guarded) | `UNGUARDED_LOOP` |
| Sub-processes have their own start and end events | compile error |
| A start event, task, call or sub-process has one outgoing flow; a flow splits only at a gateway | `SPLIT_WITHOUT_GATEWAY` |
| A boundary event has one outgoing flow, sits on a user task, sub-process or call (an error boundary on a sub-process or call), and a task has at most one timer and one message boundary | `INVALID_BOUNDARY_ATTACHMENT`, `SPLIT_WITHOUT_GATEWAY` |
| An end event has no outgoing flow | `END_EVENT_WITH_OUTGOING_FLOW` |
| Element ids are unique within a process | `DUPLICATE_ELEMENT_ID` |
| An exclusive split has a condition on every flow but one | `GATEWAY_BRANCH_WITHOUT_CONDITION` (warning) |
| A revert flow starts at an exclusive gateway, never at a receive task (a receive task may be the place a revert returns to) | `UNGUARDED_LOOP` |
| Diagram block present (Camunda Modeler adds this) | `MISSING_DIAGRAM` |

### Limits

| Limit | Value |
|---|---|
| Tasks in a workflow, counted after every call is expanded (a call counts once for itself and once for every task it brings in) | 1000 (`TASK_LIMIT_EXCEEDED`) |
| Lanes in a process | 100 |
| Trips around one revert loop | 50, checked while the instance runs: past it the instance stops with an error |
| Nested sub-processes | not supported (`NESTED_SUBPROCESS_NOT_SUPPORTED`) |
| A module that calls itself, directly or through others | `CALLED_ELEMENT_CYCLE` |
| Two processes with one `id` in a file | `DUPLICATE_PROCESS_ID` |

### Errors that appear when you publish

Saving a draft checks the structural rules above, and also the calls, the pools and the connector inputs (`MISSING_DEPT_ID`, `UNRESOLVED_CALLED_ELEMENT`, `INVALID_ZEEBE_PROPERTY`, `CONNECTOR_SECRET_AUTHORED`, `MULTIPLE_PROCESSES`, `NO_RUNNABLE_PROCESS`). Publishing also expands every call and checks the result:

| Error | Meaning |
|---|---|
| `CANDIDATE_USERS_EMPTY` | a task that people do has no default person, on the task or in the `Assignees` of the call that runs it. A warning in a draft, an error at publish; the message names the task. |
| `TASK_LIMIT_EXCEEDED` | the workflow has more than 1000 tasks once its calls are expanded |

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

### A worked example: Bid / No-Bid

The Bid-No-Bid review draws three pools: the consultant who issues the request, the review itself, and a reusable document-preparation module.

```
Pool "Consultant" (outside party, ignore)    Pool "Bid-No-Bid Review" (runs)
  Issue RFQ to Contractor                      lane Tender Business
                                                 [Sub-Process: BNB Process]
                                                    Prepare → Review → Approve
                                                 XOR "Bid Decision?"
                                                   ├─(decision = "bid")───→ Inform Depts
                                                   │                        → [Call: Handle Docs Prep]
                                                   └─(decision = "no-bid")→ Inform No Bid → End
Pool "Docs Prep" (a module with two open lanes, Sender and Receiver)
```

- The review pool is the one that runs. The consultant's pool is marked ignore, so "Issue RFQ to Contractor" is not a task anyone gets.
- "BNB Process" is a sub-process with its own start and end. Prepare, Review and Approve are user tasks stamped with the IAM template.
- The decision gateway routes on `decision`, so both outgoing flows carry a condition.
- "Handle Docs Prep" calls the module. The module has two open lanes, so the call maps them, for example `Depts` = `{"Sender":"Tender Business","Receiver":"Engineering"}`, and names a default person for each of the module's tasks in `Assignees`. A timer boundary on the call can escalate when the documents are late.

---

## Before you upload

- [ ] Every task is inside a lane
- [ ] Every task has been stamped with the IAM element template (stage type + assignment)
- [ ] Send/receive tasks reference a `<bpmn:message>` definition
- [ ] XOR gateways with multiple forward paths have a condition on every outgoing flow but one default flow
- [ ] Revert loops always have a forward exit
- [ ] Sub-processes have start and end events inside them
- [ ] No disallowed elements (script task, business rule task, etc.)
- [ ] Every callActivity names a published library module, or a process in this file, pinned with `versionTag` where the workflow must not move with the module
- [ ] Every task has a default person, in its own `candidateUsers` or in the `Assignees` of the call that runs it
- [ ] Exactly one pool runs; outside parties' pools and lanes are marked `ignore`
- [ ] Every boundary event's path starts with a task or goes straight to an end event, and an error boundary sits on a sub-process or a call
- [ ] A connector task names no credential
- [ ] The workflow has fewer than 1000 tasks once calls are expanded
