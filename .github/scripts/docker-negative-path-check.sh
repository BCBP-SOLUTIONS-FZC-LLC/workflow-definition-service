#!/usr/bin/env bash
# Fails unless the given image exits non-zero when run with no env vars set
# (DATABASE_URL is required, so config load should fail fast). Usage:
#   docker-negative-path-check.sh <image:tag>
set -euo pipefail

image="$1"

set +e
docker run --rm "$image"
exit_code=$?
set -e

if [ "$exit_code" -eq 0 ]; then
  echo "expected non-zero exit with no env vars set (DATABASE_URL required), got 0"
  exit 1
fi
echo "container correctly exited $exit_code on missing required config"
