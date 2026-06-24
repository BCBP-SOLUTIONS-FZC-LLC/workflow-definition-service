SHELL := /bin/bash

# Auto-load .env if present so make targets pick up DATABASE_URL etc. without
# requiring `source .env` in the shell first.
ifneq ($(wildcard .env),)
  include .env
  export
endif

SQLC_VERSION         := v1.31.1
BUF_VERSION          := v1.50.0
MOCKGEN_VERSION      := v0.6.0
GOLANGCI_VERSION     := v2.12.2
GOVULNCHECK_VERSION  := v1.1.4
GOARCHLINT_VERSION   := latest

# Docker images pulled by integration tests via testcontainers-go.
# Run `make tools-integration` once to warm the local Docker image cache.
TESTCONTAINERS_POSTGRES_IMAGE    := postgres:18-alpine
TESTCONTAINERS_LOCALSTACK_IMAGE  := localstack/localstack:3

TOOLS_DIR          := .tools
BIN_DIR            := bin
COVERAGE_DIR       := .coverage
MODULE             := github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service

SQLC               := $(TOOLS_DIR)/sqlc
BUF                := $(TOOLS_DIR)/buf
MOCKGEN            := $(TOOLS_DIR)/mockgen
GOLANGCI           := $(TOOLS_DIR)/golangci-lint
GOVULNCHECK        := $(TOOLS_DIR)/govulncheck
GOARCHLINT         := $(TOOLS_DIR)/go-arch-lint

BUILD_VERSION      ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS            := -X main.version=$(BUILD_VERSION)

COVER_PROFILE      := $(COVERAGE_DIR)/coverage.out
COVER_HTML         := $(COVERAGE_DIR)/coverage.html
# Exclude generated packages (sqlc db/, mockgen mocks/), the postgres adapter
# (integration-tested separately), and the glue codec (requires a live AWS Glue
# endpoint — not unit-testable) from the coverage denominator.
# COVER_EXCLUDE_PKG: end-anchored, used to filter `go list` package paths.
# COVER_EXCLUDE_FILE: path-prefix form, used to filter coverage profile lines (which
#   contain /package/file.go:... rather than ending at the package name).
COVER_EXCLUDE_PKG  := /postgres/db$$\|/postgres$$\|/mocks$$\|/glue$$\|/inbound/http$$
COVER_EXCLUDE_FILE := /postgres/db/\|/postgres/\|/mocks/\|/glue/\|/service/noop_logger.go\|/inbound/http/asyncapi.go\|/inbound/http/swagger
COVER_THRESHOLD    := 95  # target 97%; postgres adapter, generated pkgs, and glue codec excluded
# Per-package floors: packages not listed must meet COVER_THRESHOLD.
# gRPC adapters are excluded because server-reflection and transport-level paths
# require a live gRPC connection and are covered by integration tests instead.
# internal/adapter/inbound/http: AsyncAPI renderer is 500 lines of HTML template
# logic only exercisable via a live dev server; swagger handlers are trivially tested.
COVER_PKG_FLOORS   := internal/adapter/inbound/grpc:75 \
                      internal/adapter/inbound/http:3 \
                      internal/adapter/outbound/grpc:90 \
                      internal/adapter/outbound/http:85 \
                      internal/bpmn_compiler:90 \
                      internal/bpmn_compiler/element:75 \
                      internal/config:90

.PHONY: all tools tools-integration generate generate-proto generate-sqlc mock \
        build migrate test test-integration \
        cover cover-func cover-html cover-check cover-check-pkg \
        arch-lint lint lint-fix vuln \
        check \
        docs-serve docs-build \
        docker-up docker-down \
        clean help

all: generate build


## tools: Install all dev tooling into .tools/
tools:
	@mkdir -p $(TOOLS_DIR)
	GOBIN=$(PWD)/$(TOOLS_DIR) go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
	GOBIN=$(PWD)/$(TOOLS_DIR) go install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	GOBIN=$(PWD)/$(TOOLS_DIR) go install go.uber.org/mock/mockgen@$(MOCKGEN_VERSION)
	@curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh \
		| sh -s -- -b $(PWD)/$(TOOLS_DIR) $(GOLANGCI_VERSION)
	GOBIN=$(PWD)/$(TOOLS_DIR) go install golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
	GOBIN=$(PWD)/$(TOOLS_DIR) go install github.com/fe3dback/go-arch-lint@$(GOARCHLINT_VERSION)
	@echo "✓ tools installed to $(TOOLS_DIR)/"

## tools-integration: Pre-pull Docker images used by integration tests (testcontainers-go)
tools-integration:
	docker pull $(TESTCONTAINERS_POSTGRES_IMAGE)
	docker pull $(TESTCONTAINERS_LOCALSTACK_IMAGE)
	@echo "✓ Docker images ready for integration tests"


## generate: Run buf (proto → gen/) and sqlc (queries → postgres/db/)
generate: generate-proto generate-sqlc

generate-proto:
	@echo "→ buf generate"
	$(BUF) generate
	@echo "proto stubs written to gen/proto/{definition,execution}/v1/"

generate-sqlc:
	@echo "→ sqlc generate"
	$(SQLC) generate
	@echo "sqlc output written to internal/adapter/outbound/postgres/db/"

## mock: Regenerate GoMock stubs for all core/port interfaces
mock:
	@echo "→ mockgen"
	@mkdir -p internal/core/port/mocks
	$(MOCKGEN) -source=internal/core/port/repository.go \
	           -destination=internal/core/port/mocks/repository_mock.go \
	           -package=mocks
	$(MOCKGEN) -source=internal/core/port/publisher.go \
	           -destination=internal/core/port/mocks/publisher_mock.go \
	           -package=mocks
	$(MOCKGEN) -source=internal/core/port/cache.go \
	           -destination=internal/core/port/mocks/cache_mock.go \
	           -package=mocks
	$(MOCKGEN) -source=internal/core/port/services.go \
	           -destination=internal/core/port/mocks/services_mock.go \
	           -package=mocks
	$(MOCKGEN) -source=internal/core/port/transactor.go \
	           -destination=internal/core/port/mocks/transactor_mock.go \
	           -package=mocks
	$(MOCKGEN) -source=internal/core/port/glue.go \
	           -destination=internal/core/port/mocks/glue_mock.go \
	           -package=mocks
	@echo "✓ mocks written to internal/core/port/mocks/"


## build: Compile server binary to bin/server
build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/server ./cmd/server
	@echo "✓ binary: $(BIN_DIR)/server"

## migrate: Apply DB schema migrations (outbox + domain) and exit. Run before the server.
migrate:
	go run ./cmd/server migrate


## test: Run unit tests with race detector and coverage (internal + test/unit)
test:
	@mkdir -p $(COVERAGE_DIR)
	go test -race -count=1 \
	    -coverpkg=$$(go list ./internal/... ./cmd/... | grep -v '$(COVER_EXCLUDE_PKG)' | tr '\n' ',' | sed 's/,$$//') \
	    -coverprofile=$(COVER_PROFILE) -covermode=atomic \
	    ./internal/... ./test/unit/...
	@grep -v '$(COVER_EXCLUDE_FILE)' $(COVER_PROFILE) > $(COVER_PROFILE).filtered && mv $(COVER_PROFILE).filtered $(COVER_PROFILE)
	@go tool cover -func=$(COVER_PROFILE) | awk '/^total:/{print "total:", $$NF}'

## test-integration: Run integration tests — spins up containers via testcontainers-go (no make docker-up needed)
test-integration:
	@mkdir -p $(COVERAGE_DIR)
	AWS_ACCESS_KEY_ID=test \
	AWS_SECRET_ACCESS_KEY=test \
	AWS_EC2_METADATA_DISABLED=true \
	TESTCONTAINERS_RYUK_DISABLED=true \
	go test -race -count=1 -tags integration \
	    -coverpkg=$$(go list ./internal/... | grep -v '$(COVER_EXCLUDE_PKG)' | tr '\n' ',' | sed 's/,$$//') \
	    -coverprofile=$(COVERAGE_DIR)/coverage-integration.out \
	    -covermode=atomic \
	    ./test/integration/...
	@grep -v '$(COVER_EXCLUDE_FILE)' $(COVERAGE_DIR)/coverage-integration.out > $(COVERAGE_DIR)/coverage-integration.out.filtered && mv $(COVERAGE_DIR)/coverage-integration.out.filtered $(COVERAGE_DIR)/coverage-integration.out
	@go tool cover -func=$(COVERAGE_DIR)/coverage-integration.out | tail -1

## cover: Print total coverage from last test run (run 'make test' first)
cover:
	@[ -f $(COVER_PROFILE) ] || { echo "no profile — run 'make test' first"; exit 1; }
	@go tool cover -func=$(COVER_PROFILE) | awk '/^total:/{print "total:", $$NF}'

## cover-func: Print per-function coverage breakdown (run 'make test' first)
cover-func:
	@[ -f $(COVER_PROFILE) ] || { echo "no profile — run 'make test' first"; exit 1; }
	@go tool cover -func=$(COVER_PROFILE)

## cover-html: Open HTML coverage report in the browser (run 'make test' first)
cover-html:
	@[ -f $(COVER_PROFILE) ] || { echo "no profile — run 'make test' first"; exit 1; }
	go tool cover -html=$(COVER_PROFILE) -o $(COVER_HTML)
	@echo "✓ report: $(COVER_HTML)"
	@open $(COVER_HTML) 2>/dev/null || xdg-open $(COVER_HTML) 2>/dev/null || true

## cover-check-pkg: Per-package coverage gate — each package must meet its floor (run 'make test' first)
cover-check-pkg:
	@[ -f $(COVER_PROFILE) ] || { echo "no profile — run 'make test' first"; exit 1; }
	@awk \
	  -v module="$(MODULE)/" \
	  -v floors="$(subst \,,$(COVER_PKG_FLOORS))" \
	  -v global="$(COVER_THRESHOLD)" \
	  'BEGIN { \
	    n=split(floors,pairs," "); \
	    for(i=1;i<=n;i++){split(pairs[i],kv,":");thresh[kv[1]]=kv[2]+0} \
	  } \
	  /^mode:/{next} \
	  { \
	    key=$$1; stmts=$$2+0; count=$$3+0; \
	    blk_stmts[key]=stmts; blk_count[key]+=count; \
	    path=key; sub(/:.*$$/,"",path); sub(module,"",path); sub(/\/[^\/]+$$/,"",path); \
	    blk_pkg[key]=path \
	  } \
	  END { \
	    for(key in blk_stmts){ \
	      pkg=blk_pkg[key]; \
	      tot[pkg]+=blk_stmts[key]; \
	      if(blk_count[key]>0) cov[pkg]+=blk_stmts[key] \
	    } \
	    fail=0; \
	    for(pkg in tot){ \
	      if(tot[pkg]==0)continue; \
	      pct=cov[pkg]*100/tot[pkg]; \
	      floor=(pkg in thresh)?thresh[pkg]:global; \
	      if(pct<floor){printf "✗  %-58s %5.1f%% (need %d%%)\n",pkg,pct,floor; fail=1} \
	      else{printf "✓  %-58s %5.1f%%\n",pkg,pct} \
	    } \
	    exit fail \
	  }' $(COVER_PROFILE)

## cover-check: Global + per-package coverage gate (run 'make test' first)
cover-check: cover-check-pkg
	@[ -f $(COVER_PROFILE) ] || { echo "no profile — run 'make test' first"; exit 1; }
	@TOTAL=$$(go tool cover -func=$(COVER_PROFILE) | awk '/^total:/{print $$NF}' | tr -d '%'); \
	echo "total: $${TOTAL}% (floor: $(COVER_THRESHOLD)%)"; \
	if [ $$(echo "$${TOTAL} < $(COVER_THRESHOLD)" | bc -l) -eq 1 ]; then \
		echo "✗ total coverage below $(COVER_THRESHOLD)%"; exit 1; \
	else \
		echo "✓ coverage ok"; \
	fi


## check: Run vet, arch-lint, lint, unit tests, integration tests, and coverage gate — full local CI pass
check:
	@echo "==> gofmt"
	@files=$$(gofmt -l .); if [ -n "$$files" ]; then echo "gofmt violations:"; echo "$$files"; exit 1; fi
	@echo "==> go vet"
	go vet ./...
	@echo "==> arch-lint"
	$(MAKE) arch-lint
	@echo "==> lint"
	$(GOLANGCI) run ./...
	@echo "==> test + coverage gate"
	$(MAKE) test
	$(MAKE) cover-check
	@echo "==> integration tests"
	$(MAKE) test-integration
	@echo "✓ all checks passed"

## arch-lint: Enforce Clean Architecture import direction via go-arch-lint
arch-lint:
	$(GOARCHLINT) check --project-path .

## lint: Run golangci-lint (read-only; exits non-zero on violations)
lint:
	$(GOLANGCI) run ./...

## lint-fix: Run golangci-lint with auto-fix
lint-fix:
	$(GOLANGCI) run --fix ./...

## vuln: Run govulncheck to detect known vulnerabilities in dependencies
vuln:
	$(GOVULNCHECK) ./...


## docs-serve: Serve MkDocs locally at http://localhost:8001  (requires: brew install mkdocs)
docs-serve:
	mkdocs serve --dev-addr 0.0.0.0:8001

## docs-build: Build static MkDocs site to site/
docs-build:
	mkdocs build


## docker-up: Start local infra (PostgreSQL + Valkey)
docker-up:
	docker compose up -d
	@echo "postgres on :5432, valkey on :6379"

## docker-down: Stop local infra
docker-down:
	docker compose down


## clean: Remove build output, generated files, and coverage reports
clean:
	rm -rf $(BIN_DIR)
	rm -rf $(COVERAGE_DIR)
	rm -rf site/
	rm -rf gen/
	rm -rf internal/adapter/outbound/postgres/db/
	rm -rf internal/core/port/mocks/


## help: List available targets
help:
	@grep -E '^## ' Makefile | sed 's/## /  /'
