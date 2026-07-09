#!/usr/bin/env bash
# Runs the unit test suite with race detection and coverage, then filters
# generated/integration-only packages out of the coverage profile.
set -euo pipefail

mkdir -p .coverage
EXCLUDE='/postgres/db$\|/postgres$\|/mocks$\|/glue$\|/inbound/http$'
go test -race -count=1 \
  -coverpkg="$(go list ./internal/... ./cmd/... | grep -v "$EXCLUDE" | tr '\n' ',' | sed 's/,$//')" \
  -coverprofile=.coverage/coverage.out -covermode=atomic \
  ./internal/... ./test/unit/...

grep -v '/postgres/db/\|/postgres/\|/mocks/\|/glue/\|/service/noop_logger.go\|/inbound/http/asyncapi.go\|/inbound/http/swagger' \
  .coverage/coverage.out > .coverage/coverage.out.filtered
mv .coverage/coverage.out.filtered .coverage/coverage.out

go tool cover -func=.coverage/coverage.out | tail -1
