# Schema Governance

This service publishes one outbound domain event today (`workflow.template.published`) and treats its wire contract as a versioned, governed artifact rather than an implicit side effect of whatever the Go struct happens to look like. `platform-schemagov` — a CLI distributed as a Docker image (`ghcr.io/bcbp-solutions-fzc-llc/platform-schemagov`) — is the tool that enforces this end to end: locally during development and again in CI on every push.

## Source of truth

```text
api/asyncapi.yaml   →  make extract-schemas  →  internal/eventschema/*.json
```

`api/asyncapi.yaml` is authored by hand and is the canonical description of every event this service emits, including `x-lifecycle` and `x-owner` governance annotations per message. `internal/eventschema/*.json` is a derived, *committed* artifact — one Draft-07 JSON Schema file per event — extracted from the AsyncAPI spec and checked into the repo so it can be diffed, validated, and registered independently of the Go source.

Both are tracked in git (`internal/eventschema/` is deliberately not in `.gitignore`, unlike the other generated directories in this repo) precisely because schema evolution needs its own review and audit trail, separate from application code changes.

## Local commands

```bash
make schema-pull        # pull the platform-schemagov image
make extract-schemas    # api/asyncapi.yaml → internal/eventschema/*.json
make schema-validate    # 8-pass structural/lifecycle/drift validation, no AWS required
make schema-diff CURRENT=<f> PROPOSED=<f> [SCHEMA_NAME=<name>]   # pure file-to-file diff
make schema-register     # register into AWS Glue Schema Registry (needs AWS creds or LocalStack)
make schema-prune        # report orphaned Glue schemas (EXECUTE=true to actually delete)
```

`make schema-validate` runs entirely offline against the committed files — no AWS credentials needed — which is what makes it safe to run as a fast local check or a read-only CI gate before any registry call happens.

## Environment variables

| Variable | Used by | Notes |
| --- | --- | --- |
| `GLUE_REGISTRY_NAME` | Running service (`internal/config`) **and** CI tooling | Required at runtime when `AWS_USE_STUB=false` — the service's Glue codec resolves schemas against this registry when decoding/encoding events. |
| `GLUE_REGISTRY_ARN` | CI/schema-gov tooling only | **Not read by the running service** — there is no corresponding field on `internal/config.Config`. It scopes IAM policy for the `schema-register`/`schema-prune` pipeline steps, not application behavior. Don't confuse it with a runtime requirement. |
| `SCHEMA_GOV_IMAGE` | `make schema-*` targets and CI | Pins the `platform-schemagov` image tag used by every schema command; not read by the server binary at all. |

## CI workflows

Four workflows implement the full lifecycle, each with a distinct, narrow responsibility:

### `schema-registry.yml` — validate, diff, register

Triggers: PR into `main` touching schema files (`pr-check`, read-only), push to `main` (`staging`, full pipeline), a published release (`production`), or manual dispatch.

The full (staging/production) pipeline: validate → check for an active schema freeze → assess event usage against CloudWatch/Prometheus (flagging events nobody has emitted, `NEVER_SEEN`, for deprecation) → diff against what's already registered (fails the run on a breaking change *before* anything is uploaded) → register (idempotent create-or-new-version) → append a dated changelog entry → emit metrics. The `pr-check` job runs only the read-only half (validate + diff against the staging registry) so a reviewer sees compatibility problems before merge, without ever touching the registry.

### `schema-prune.yml` — retire orphaned schemas

A schema becomes a prune candidate when it's registered in Glue but has no corresponding file in `internal/eventschema/` (i.e. it was retired and its JSON file removed). Runs monthly as a dry-run report only; actually deleting requires an explicit manual dispatch with `dry_run=false` — production pruning is never scheduled automatically, since `glue:DeleteSchema` is irreversible for any consumer still pinned to a version UUID. Executed prunes archive every version definition to `docs/schema-archive/<schema-name>/` before deleting from Glue.

### `schema-health-quarterly.yml` — health review prompt

A read-only quarterly report (version accumulation per schema, overdue deprecations, stale lifecycle annotations) surfaced as a GitHub Step Summary for a human to act on. It never mutates anything itself — follow-up actions go through `schema-prune.yml` or a normal schema PR.

### `freeze-watchdog.yml` — guard against a forgotten freeze

`SCHEMA_FREEZE` is an environment variable that blocks registration (used during incident response or planned migrations). This workflow polls its age every few hours and escalates from a warning to a hard failure if it's been left on far longer than any real freeze window should last — protecting against exactly the failure mode of "someone set it during an incident and forgot to unset it."

## Governance guardrails

- **CODEOWNERS**: changes to `api/asyncapi.yaml` and `internal/eventschema/` require review from the platform-engineers/platform-team owners, not just any approver.
- **Drift gate**: `validate-test.yml` runs `extract-schemas --check` on every push — if the committed JSON Schema files don't match what `api/asyncapi.yaml` would currently produce, CI fails. The two can never silently diverge.
- **Breaking-change gate**: the `diff` step in `schema-registry.yml` fails the pipeline before any registration if a proposed schema isn't backward-compatible with what's already live — evolution has to go through a versioned type (e.g. `workflow.template.published.v2`), not an in-place incompatible edit.
