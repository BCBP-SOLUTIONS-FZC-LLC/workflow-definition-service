#!/usr/bin/env bash
# Starts or stops the stack the black-box checks (make hurl, make fuzz-api) run against: Postgres and
# Valkey from the compose file, the three stubs under cmd/stub (membership, catalog, execution) and the
# Definition Service. The checks write data, so use a throwaway database (the compose one is).
#
#   scripts/blackbox-stack.sh up     build and start everything, wait until it answers
#   scripts/blackbox-stack.sh down   stop everything
#
# Ports (the .env.example defaults): 8080 API, 9091 metrics, 8081 membership stub, 8083 execution
# stub, 8084 catalog stub. Logs and binaries go to .coverage/blackbox.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
work="$root/.coverage/blackbox"
pids="$work/pids"

wait_listening() {
  local name="$1" port="$2" log="$3"
  for _ in $(seq 1 90); do
    if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then return 0; fi
    sleep 1
  done
  echo "$name did not listen on :$port" >&2
  tail -n 30 "$log" >&2 || true
  return 1
}

wait_ok() {
  local name="$1" url="$2" log="$3"
  for _ in $(seq 1 90); do
    if curl -fsS -o /dev/null "$url" 2>/dev/null; then return 0; fi
    sleep 1
  done
  echo "$name did not answer 200 on $url" >&2
  tail -n 30 "$log" >&2 || true
  return 1
}

start() {
  local name="$1"
  shift
  "$@" >"$work/$name.log" 2>&1 &
  echo $! >>"$pids"
}

up() {
  mkdir -p "$work/bin"
  : >"$pids"
  [ -f "$root/.env" ] || cp "$root/.env.example" "$root/.env"

  (cd "$root" && docker compose -p definition_service up -d --wait --wait-timeout 180 postgres valkey)
  (cd "$root" && go run ./cmd/server migrate >"$work/migrate.log" 2>&1)

  for stub in membership catalog execution; do
    (cd "$root" && go build -o "$work/bin/stub-$stub" "./cmd/stub/$stub")
  done
  (cd "$root" && go build -o "$work/bin/server" ./cmd/server)

  for stub in membership catalog execution; do
    start "stub-$stub" "$work/bin/stub-$stub"
  done
  start server "$work/bin/server"

  wait_listening "membership stub" 8081 "$work/stub-membership.log"
  wait_listening "execution stub" 8083 "$work/stub-execution.log"
  wait_listening "catalog stub" 8084 "$work/stub-catalog.log"
  wait_ok "Definition Service" http://localhost:8080/readyz "$work/server.log"
  echo "black-box stack is up"
}

down() {
  if [ -f "$pids" ]; then
    while read -r pid; do kill "$pid" 2>/dev/null || true; done <"$pids"
    : >"$pids"
  fi
  (cd "$root" && docker compose -p definition_service stop >/dev/null 2>&1) || true
}

case "${1:-}" in
up) up ;;
down) down ;;
*)
  echo "usage: $0 up|down" >&2
  exit 2
  ;;
esac
