#!/usr/bin/env bash
# Installs sqlc/mockgen (if not already present, e.g. via a warm .tools cache)
# and regenerates protobuf, sqlc, and mockgen output. Run after buf-setup-action.
set -euo pipefail

SQLC_VERSION="v1.31.1"
MOCKGEN_VERSION="v0.6.0"

if [ ! -x .tools/sqlc ] || [ ! -x .tools/mockgen ]; then
  echo "installing sqlc ${SQLC_VERSION} and mockgen ${MOCKGEN_VERSION}"
  GOBIN="$PWD/.tools" go install "github.com/sqlc-dev/sqlc/cmd/sqlc@${SQLC_VERSION}"
  GOBIN="$PWD/.tools" go install "go.uber.org/mock/mockgen@${MOCKGEN_VERSION}"
fi

buf generate
.tools/sqlc generate

mkdir -p internal/core/port/mocks
.tools/mockgen -source=internal/core/port/repository.go -destination=internal/core/port/mocks/repository_mock.go -package=mocks
.tools/mockgen -source=internal/core/port/publisher.go  -destination=internal/core/port/mocks/publisher_mock.go  -package=mocks
.tools/mockgen -source=internal/core/port/cache.go      -destination=internal/core/port/mocks/cache_mock.go      -package=mocks
.tools/mockgen -source=internal/core/port/services.go   -destination=internal/core/port/mocks/services_mock.go   -package=mocks
.tools/mockgen -source=internal/core/port/transactor.go -destination=internal/core/port/mocks/transactor_mock.go -package=mocks
.tools/mockgen -source=internal/core/port/glue.go       -destination=internal/core/port/mocks/glue_mock.go       -package=mocks
