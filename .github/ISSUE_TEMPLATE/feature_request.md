---
name: Feature request
about: Propose a new feature for the workflow-definition-service (new endpoint, BPMN validation rule, event type, compiler behaviour, observability, etc.)
title: '[FEAT] '
labels: enhancement
assignees: ''
---

## Problem / motivation

What problem does this solve? Which user persona is affected (tenant admin, platform engineer, downstream service)?

## Proposed solution

Describe the API change, new behaviour, or new event type you'd like.

```go
// Example: new handler signature, domain type, or port interface change
```

## Design constraints

- Does this require a database migration?
- Does this introduce a new outbound event type (add to `wf.template.*` namespace)?
- Does this affect the gRPC `GetCompiledWorkflow` contract used by the Execution Service?
- Does this change BPMN validation rules (affects existing published templates)?

## Alternatives considered

Other approaches you evaluated and why you ruled them out.

## Acceptance criteria

- [ ]
- [ ]
- [ ]

## Additional context

Links to related issues, design docs (`.design/definition_service.md` section), or prior art.
