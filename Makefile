SHELL := /bin/bash

# Auto-load .env if present so make targets pick up DATABASE_URL etc. without
# requiring `source .env` in the shell first.
ifneq ($(wildcard .env),)
  include .env
  export
endif

SQLC_VERSION       := latest
GOOSE_VERSION      := latest
BUF_VERSION        := latest
MOCKGEN_VERSION    := latest
GOLANGCI_VERSION   := latest

TOOLS_DIR          := .tools
BIN_DIR            := bin
COVERAGE_DIR       := .coverage
MODULE             := github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service

SQLC               := $(TOOLS_DIR)/sqlc
GOOSE              := $(TOOLS_DIR)/goose
BUF                := $(TOOLS_DIR)/buf
MOCKGEN            := $(TOOLS_DIR)/mockgen
GOLANGCI           := $(TOOLS_DIR)/golangci-lint

BUILD_VERSION      ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS            := -X main.version=$(BUILD_VERSION)

COVER_PROFILE      := $(COVERAGE_DIR)/coverage.out
COVER_HTML         := $(COVERAGE_DIR)/coverage.html
COVER_THRESHOLD    := 70

.PHONY: all tools generate generate-proto generate-sqlc mock \
        migrate-up migrate-down \
        build test test-integration \
        cover cover-html cover-check \
        lint \
        docs-serve docs-build \
        docker-up docker-down \
        clean help

all: generate build


## tools: Install all dev tooling into .tools/
tools:
	@mkdir -p $(TOOLS_DIR)
	GOBIN=$(PWD)/$(TOOLS_DIR) go install github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
	GOBIN=$(PWD)/$(TOOLS_DIR) go install github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)
	GOBIN=$(PWD)/$(TOOLS_DIR) go install github.com/bufbuild/buf/cmd/buf@$(BUF_VERSION)
	GOBIN=$(PWD)/$(TOOLS_DIR) go install go.uber.org/mock/mockgen@$(MOCKGEN_VERSION)
	@curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/HEAD/install.sh \
		| sh -s -- -b $(PWD)/$(TOOLS_DIR) $(GOLANGCI_VERSION)
	@echo "✓ tools installed to $(TOOLS_DIR)/"


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
	@echo "✓ mocks written to internal/core/port/mocks/"


## migrate-up: Apply all pending Goose migrations
migrate-up:
	$(GOOSE) -dir db/migrations postgres "$(DATABASE_URL)" up

## migrate-down: Roll back the last applied Goose migration
migrate-down:
	$(GOOSE) -dir db/migrations postgres "$(DATABASE_URL)" down


## build: Compile server binary to bin/server
build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/server ./cmd/server
	@echo "✓ binary: $(BIN_DIR)/server"


## test: Run unit tests with race detector
test:
	go test -race -count=1 ./...

## test-integration: Run integration tests (requires running infra)
test-integration:
	go test -race -count=1 -tags=integration ./...

## cover: Run tests and print per-package coverage summary
cover:
	@mkdir -p $(COVERAGE_DIR)
	go test -race -count=1 -coverprofile=$(COVER_PROFILE) -covermode=atomic ./...
	@go tool cover -func=$(COVER_PROFILE) | tail -1

## cover-html: Open an HTML coverage report in the browser
cover-html: cover
	go tool cover -html=$(COVER_PROFILE) -o $(COVER_HTML)
	@echo "✓ report: $(COVER_HTML)"
	@open $(COVER_HTML) 2>/dev/null || xdg-open $(COVER_HTML) 2>/dev/null || true

## cover-check: Fail if total coverage is below COVER_THRESHOLD (default 70%)
cover-check: cover
	@TOTAL=$$(go tool cover -func=$(COVER_PROFILE) | tail -1 | awk '{print $$3}' | tr -d '%'); \
	echo "Coverage: $${TOTAL}% (threshold: $(COVER_THRESHOLD)%)"; \
	if [ $$(echo "$${TOTAL} < $(COVER_THRESHOLD)" | bc -l) -eq 1 ]; then \
		echo "✗ coverage below $(COVER_THRESHOLD)%"; exit 1; \
	else \
		echo "✓ coverage ok"; \
	fi


## lint: Run golangci-lint
lint:
	$(GOLANGCI) run ./...


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
