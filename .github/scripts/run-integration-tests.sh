#!/usr/bin/env bash
# Runs the integration test suite (testcontainers, real Postgres/LocalStack)
# with race detection and coverage, then filters the profile.
set -euo pipefail

mkdir -p .coverage
EXCLUDE='/postgres/db$\|/postgres$\|/mocks$\|/glue$\|/inbound/http$'
go test -race -count=1 -tags integration \
  -coverpkg="$(go list ./internal/... | grep -v "$EXCLUDE" | tr '\n' ',' | sed 's/,$//')" \
  -coverprofile=.coverage/coverage-integration.out \
  -covermode=atomic \
  ./test/integration/... ./test/e2e/...

grep -v '/postgres/db/\|/postgres/\|/mocks/\|/glue/\|/service/noop_logger.go\|/inbound/http/asyncapi.go\|/inbound/http/swagger' \
  .coverage/coverage-integration.out > .coverage/coverage-integration.out.filtered
mv .coverage/coverage-integration.out.filtered .coverage/coverage-integration.out

go tool cover -func=.coverage/coverage-integration.out | tail -1
