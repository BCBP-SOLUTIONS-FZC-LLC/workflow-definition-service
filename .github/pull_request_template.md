## Pull Request Checklist

Thank you for your contribution! Please complete the checklist below before requesting review.

### Description
<!-- A clear, concise summary of what this PR changes and why. -->

### Type of change

- [ ] Bug fix (non-breaking)
- [ ] New feature (non-breaking)
- [ ] Breaking change (requires version bump + migration guide)
- [ ] Refactor / cleanup
- [ ] Documentation / config only

---

### Code checklist

- [ ] `make lint` passes with no new warnings
- [ ] `make test` passes (unit tests with race detector)
- [ ] `make test-integration` passes (requires Docker)
- [ ] `make cover-check` passes ≥ 95% threshold
- [ ] New code follows the Clean Architecture import rules (`domain ← port ← service ← adapter`)
- [ ] No `internal/core/` code imports from `adapter/`

### Database migrations

- [ ] No new migrations in this PR  
- OR:
- [ ] Migration added to `db/migrations/` as a `NNNNNN_name.up.sql` + `.down.sql` pair (golang-migrate; no `+goose` annotations)
- [ ] Migration is safe to run on a live database (backward-compatible, no locking full-table rewrites on large tables)
- [ ] Verified the migration applies at startup (`go run ./cmd/server`) and via `make test-integration`

### Events / messaging

- [ ] No event changes in this PR  
- OR:
- [ ] New event type documented in `.claude/CLAUDE.md` (outbound event types section)
- [ ] Outbound events use `outbox.Enqueue` inside a `pgcommon.RunInTx` callback — never `publisher.Publish` directly
- [ ] Inbound event handling (`POST /internal/events`) is idempotent (checks `processed_event` before acting)
- [ ] `api/asyncapi.yaml` updated with `x-lifecycle`/`x-owner` on any new/changed message; `make extract-schemas` re-run so `internal/eventschema/*.json` matches (CI's `extract --check` gate will otherwise fail)

### Code generation

- [ ] No changes to proto, sqlc queries, or port interfaces  
- OR:
- [ ] `make generate` was re-run after changing `.proto` or `db/queries/*.sql`
- [ ] `make mock` was re-run after changing `internal/core/port/*.go` interfaces
- [ ] Generated files are **not** committed (they are gitignored and regenerated in CI)

### Tests

- [ ] Unit tests added or updated for changed behaviour
- [ ] Integration tests added for any new repository methods
- [ ] No `t.Skip` placeholders removed without implementing the test body

### Documentation

- [ ] README updated if setup steps or environment variables changed
- [ ] `.claude/CLAUDE.md` updated if architecture or patterns changed
- [ ] CHANGELOG.md entry added under `## [Unreleased]`

### Security

- [ ] No secrets, tokens, or DSNs in committed code
- [ ] Request context (`x-tenant-id`, `x-user-id`) is propagated to all DB queries via RLS GUC
- [ ] BPMN XML inputs are parsed with XXE protection (no `xml.Unmarshal` on untrusted input without the hardened decoder)

---

### Screenshots / logs (if applicable)
<!-- Paste relevant log output, curl responses, or screenshots here. -->
