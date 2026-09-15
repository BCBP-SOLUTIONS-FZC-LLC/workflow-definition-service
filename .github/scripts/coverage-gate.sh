#!/usr/bin/env bash
# Enforces the global coverage threshold (default 95%) against an existing
# .coverage/coverage.out profile. Writes `pct` to GITHUB_OUTPUT when set.
set -euo pipefail

THRESHOLD="${COVER_THRESHOLD:-95}"

pct=$(go tool cover -func=.coverage/coverage.out | tail -1 | awk '{print $3}' | tr -d '%')
echo "Total coverage: ${pct}%"

if [ -n "${GITHUB_OUTPUT:-}" ]; then
  echo "pct=${pct}%" >> "$GITHUB_OUTPUT"
fi

awk -v p="$pct" -v t="$THRESHOLD" 'BEGIN { if (p+0 < t+0) { print "FAIL: " p "% is below the " t "% threshold"; exit 1 } else { print "PASS: " p "%" } }'
