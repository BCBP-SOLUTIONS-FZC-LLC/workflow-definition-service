# UI Enrichment Guide

This guide is for the operations or engineering team that reviews and activates compiled workflow plans. Before a plan can go live, identity values must be verified against IAM and any placeholder values must be replaced.

---

## Who this is for

Engineers or ops staff who:

- Review compiled plan JSON produced by the Definition Service after a designer uploads a BPMN file
- Verify that department IDs and user UUIDs match real IAM records
- Sign off before a plan is published and the Execution Service starts routing tasks

Designers (who model the BPMN in Camunda Modeler) do **not** do this work. Stage type identifiers and `candidateGroups` are set by IAM-owned Camunda Modeler element templates and must not be modified here.

---

## Compiled plan identity fields

The compiled plan is a JSON document returned by the Definition Service. The fields relevant to enrichment are:

### `DepartmentDef.id`

The department slug derived verbatim from the BPMN lane name. It must match an IAM department slug exactly — casing and hyphens included.

```json
{ "id": "design-review", "stages": [...] }
```

**Do not rename** a department ID after a workflow goes live without coordinating with engineering and IAM. Running executions reference the ID by string.

### `StageDef.default_assignees`

A list of UUID v7 values identifying the default assignees for a stage. The Execution Service validates the format at runtime.

```json
{ "default_assignees": ["018f4e3a-ab12-7c9d-b456-9e3f1a2b3c4d"] }
```

Source each UUID from the IAM user profile service — look up by username or email. Do not abbreviate, truncate, or invent values.

### `StageDef.extras`

Key/value pairs forwarded directly from `zeebe:properties` in the BPMN. These are owned by the designer and engineering team. **Do not modify `extras` values** during enrichment; they control engine behaviour such as SLA thresholds and notification templates.

### `StageDef.node_id`

The BPMN element ID (e.g. `Activity_0abc123`). Read-only — used by the Execution Service for stable routing. Never modify.

---

## User UUIDs

| Requirement | Detail |
|---|---|
| Format | UUID v7 (time-ordered), e.g. `018f4e3a-ab12-7c9d-b456-9e3f1a2b3c4d` |
| Source | IAM user profile service — query by username or email |
| Validation | The Execution Service rejects malformed UUIDs at task-claim time |
| Placeholders | Any value matching `<...>` or containing only zeros (other than the nil UUID) is a placeholder — replace it |

---

## Department IDs

Department IDs come from lane names set by the BPMN designer. Before activation:

1. List all `DepartmentDef.id` values in the compiled plan.
2. Cross-check each against the IAM department slug list.
3. If a slug does not exist in IAM, raise it with the designer — the BPMN lane name must be corrected and the file re-uploaded. Do not patch the compiled JSON.

---

## Condition expression variables

Condition expressions on exclusive gateways (e.g. `= decision == "approved"`) use variable names agreed between the designer and engineering at design time. The enrichment team verifies that the variable names in the plan match what the Execution Service will supply — they do not define the logic.

If a variable name looks wrong, raise it with engineering before activating the plan.

---

## Activation checklist

Run through this before marking a plan as ready for publishing:

- [ ] All `default_assignees` entries contain real UUID v7 values sourced from IAM
- [ ] No placeholder strings (e.g. `"<user-uuid>"`, all-zero UUIDs) remain in the plan JSON
- [ ] All `DepartmentDef.id` values match an IAM department slug exactly
- [ ] `extras` values have not been modified from what the designer set
- [ ] Condition expression variable names have been verified with engineering
- [ ] A second reviewer has signed off on UUID and department ID correctness
