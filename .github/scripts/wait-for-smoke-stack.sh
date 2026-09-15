#!/usr/bin/env bash
# Starts the membership/execution stubs and the server in the background,
# then blocks until all three report healthy (60s deadline each).
set -euo pipefail

go run ./cmd/stub/membership &
go run ./cmd/stub/execution &
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

wait_until "execution stub" curl -sf -X POST http://localhost:9092/control \
  -H 'Content-Type: application/json' -d '{"has_active":false}'

wait_until "server" curl -sf http://localhost:8080/healthz
