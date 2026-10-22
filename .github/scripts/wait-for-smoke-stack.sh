#!/usr/bin/env bash
# Starts the membership/execution/catalog stubs and the server in the background,
# then blocks until all four report healthy (60s deadline each).
set -euo pipefail

go run ./cmd/stub/membership &
go run ./cmd/stub/execution &
go run ./cmd/stub/catalog &
go run ./cmd/server &

wait_until() {
  local desc="$1" deadline=$((SECONDS + 60))
  shift
  until "$@" &>/dev/null; do
    if [ "$SECONDS" -ge "$deadline" ]; then
      echo "Timed out waiting for $desc"
      exit 1
    fi
    sleep 1
  done
}

wait_until "membership stub" curl -sf -X POST http://localhost:8081/control \
  -H 'Content-Type: application/json' -d '{"eligible":true}'

wait_until "execution stub" curl -sf -X POST http://localhost:8083/control \
  -H 'Content-Type: application/json' -d '{"has_active":false}'

wait_until "catalog stub" curl -sf http://localhost:8084/api/v1/internal/plans \
  -H 'x-tenant-roles: iam-system'

wait_until "server" curl -sf http://localhost:8080/healthz
