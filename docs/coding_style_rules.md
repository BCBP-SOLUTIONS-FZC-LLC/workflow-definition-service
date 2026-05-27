# Go Coding Style & Formatting Rules

This document defines the official coding style and formatting standards for all backend Go services in the platform. These rules are designed to ensure codebase consistency, high maintainability, clean telemetry integration, and robust testing patterns.

---

## 1. Code Layout & Formatting

### 1.1 Line Length

* **Limit**: Maximum **100 characters** per line.
* Long lines (e.g., function signatures, struct instantiations, string concatenations) must be wrapped readable when they exceed this limit.

### 1.2 File Length & Splitting

* **Limit**: There is no hard limit on the number of lines per file.
* **Rule**: Split files when they become **structurally unwieldy** (e.g., when a file spans too many unrelated domains, contains a mix of core models and adapter logic, or makes navigation difficult).
* Keep core business entities separated from concrete delivery adapters (enforced by Clean Architecture).

### 1.3 Import Grouping

Imports must be grouped into exactly **three blocks**, separated by a single empty line, in the following order:

1. **Standard Library**: Packages from Go's standard library (e.g., `context`, `errors`, `fmt`).
2. **Third-Party Libraries**: External dependencies (e.g., `github.com/gin-gonic/gin`, `github.com/google/uuid`).
3. **Internal Packages**: Local project packages (e.g., `github.com/org/workflow-service/internal/core/domain`).

Example:

```go
import (
 "context"
 "fmt"
 "time"

 "github.com/google/uuid"
 "go.uber.org/multierr"

 "github.com/org/workflow-service/internal/core/domain"
 "github.com/org/workflow-service/internal/core/port"
)
```

---

## 2. Function & Method Design

### 2.1 Function Complexity & Length

* **Cognitive Complexity**: Keep cognitive complexity **below 15**. Developers should install and run the **SonarQube** linting plugin within their IDE to check complexity before committing.
* **Length**: No strict line-of-code (LOC) limit is enforced if a function's logic inherently requires it, but functions exceeding 80 lines should be evaluated for potential decomposition into smaller helpers.

### 2.2 Method Grouping & Ordering

* **Grouping**: All methods belonging to a specific struct must be grouped consecutively within the same Go file.
* **Ordering**: Sort methods either **alphabetically** or by **visibility** (public methods listed first, followed by the private helper methods they invoke).

---

## 3. Naming Conventions

All symbols must adhere to **strict, idiomatic Go naming conventions**:

* **camelCase**: Used for unexported (private) struct fields, local variables, and private functions/methods (e.g., `userID`, `workflowDef`, `activeDraft`).
* **PascalCase**: Used for exported (public) structs, interfaces, fields, and functions/methods (e.g., `WorkflowRepository`, `TemplatePublished`, `GetCompiledWorkflow`).
* **Initialisms & Acronyms**: Must be fully capitalized to maintain readability (e.g., `JSON`, `XML`, `URL`, `ID`, `UUID`, `BPMN`, `RLS`, `GUC`, `mTLS`, `SNS`, `SQS`, `AST`, `DSL`).
  * *Correct*: `userID`, `workflowXML`, `GetWorkflowByID`
  * *Incorrect*: `userId`, `workflowXml`, `GetWorkflowById`

---

## 4. Comments & Documentation

The codebase prioritizes **self-documenting code** over extensive commenting.

* **Minimize Comments**: Do not write comments unless they are absolutely necessary. Rely on descriptive variable names, clean control flows, and visible, self-explanatory logic.
* **Documenting the Obscure**: If a code block is obscure or relies on specific low-level behavior (e.g., complex graph operations, database locking hints, or reflection), write a brief, high-level comment explaining *why* it is written this way.
* **References**: For complex background logic, include brief references pointing to the corresponding architectural designs or external documentation inside the comment block.

---

## 5. Error Handling & Structured Logging

### 5.1 Structured Logging (Zap)

* **Framework**: Use `go.uber.org/zap` for all structured logging, provided via `platform-gincommon/pkg/logger`. Obtain a `*zap.Logger` instance once at startup (via `logger.NewLogger(env).(*zap.Logger)`) and pass it down through constructor injection. Do not use global loggers (`zap.L()`, `zap.S()`).
* **Metric Integration**: All error scenarios must emit structured logs to ensure easy consumption by telemetry and monitoring tools (e.g., Prometheus, Grafana).
* **Attribute Binding**: Do not format variable contexts into string messages. Pass them as typed Zap fields:
  * *Correct*: `log.Error("failed to publish workflow draft", zap.Error(err), zap.String("tenant_id", tenantID))`
  * *Incorrect*: `log.Error(fmt.Sprintf("failed to publish workflow draft for tenant %s: %v", tenantID, err))`

### 5.2 Error Formatting & Context

* **Wrapping**: Wrap errors with meaningful context as they traverse boundary layers using `fmt.Errorf("context: %w", err)`.
* **Sentinel Errors**: Define static sentinel errors at the package or service level (e.g., `var ErrWorkflowNotFound = errors.New("workflow not found")`) to allow callers to verify failures using `errors.Is`.

---

## 6. Unit Testing & Mocking

### 6.1 Test File Organization

* **Unit Tests**: Place unit tests in standard `*_test.go` files adjacent to the source code being tested.
* **Integration Tests**: Isolate integration tests (e.g., testing database drivers, live SQS listeners) into files matching `*_integration_test.go` and protect them using build tags to keep normal unit tests fast:

  ```go
  //go:build integration
  
  package postgres_test
  ```

### 6.2 Table-Driven Testing

* **Pattern**: Always write unit tests as **table-driven tests**. Define test cases within a slice of anonymous structs to handle happy-path and failure scenarios uniformly.

Example:

```go
func TestValidateKey(t *testing.T) {
 tests := []struct {
  name    string
  key     string
  wantErr bool
 }{
  {
   name:    "valid key",
   key:     "tender-review",
   wantErr: false,
  },
  {
   name:    "invalid characters",
   key:     "tender/review",
   wantErr: true,
  },
 }

 for _, tt := range tests {
  t.Run(tt.name, func(t *testing.T) {
   err := ValidateKey(tt.key)
   if (err != nil) != tt.wantErr {
    t.Errorf("ValidateKey() error = %v, wantErr %v", err, tt.wantErr)
   }
  })
 }
}
```

### 6.3 Mocking Conventions

* **Mocks**: Use **third-party mock generators** (e.g., Mockery, GoMock) to automatically construct mock implementations of repository and outbound client interfaces (`core/port/*`).
* Avoid writing mock implementations manually to minimize boilerplate and prevent mock maintenance drift.
