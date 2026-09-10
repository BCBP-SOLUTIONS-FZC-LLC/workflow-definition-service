# Architecture diagrams

Standalone Mermaid sources for the diagrams embedded in [`../../ARCHITECTURE.md`](../../ARCHITECTURE.md). Each diagram lives here as a `.mmd` file and is also embedded inline in `ARCHITECTURE.md` as a fenced ` ```mermaid ` block, preceded by a `> Source: docs/architecture/mermaid/<file>.mmd` back-reference — edit the `.mmd` file and copy the change into `ARCHITECTURE.md` (or vice versa); the two must stay in sync.

Render locally with the [Mermaid VS Code / IntelliJ plugin](https://marketplace.visualstudio.com/items?itemName=bierner.markdown-mermaid), [mermaid.live](https://mermaid.live), or view `ARCHITECTURE.md` directly on GitHub (native Mermaid rendering).

| File | `ARCHITECTURE.md` section |
| --- | --- |
| `system-context.mmd` | System context |
| `system-view-component.mmd` | Component diagrams → High-level system view |
| `component-class-diagram.mmd` | Component diagrams → Component class diagram |
| `layer-model.mmd` | Layer model |
| `package-dependency-graph.mmd` | Package dependency graph |
| `http-mutation-flow.mmd` | Key runtime flows → HTTP mutation (e.g. PublishVersion) |
| `grpc-get-compiled-workflow-flow.mmd` | Key runtime flows → gRPC GetCompiledWorkflow |
| `membership-revoked-flow.mmd` | Key runtime flows → DepartmentMembershipRevoked |
| `archive-workflow-flow.mmd` | Key runtime flows → Archive workflow |
| `outbox-relay-flow.mmd` | Key runtime flows → Outbox relay (high-level) |
| `outbox-poll-cycle.mmd` | Outbox pattern → Outbox poll cycle (detailed state machine) |
| `pgcommon-withconn-flow.mmd` | platform-pgcommon → Connection lifecycle → `WithConn` |
| `pgcommon-runintx-flow.mmd` | platform-pgcommon → Connection lifecycle → `RunInTx` |
