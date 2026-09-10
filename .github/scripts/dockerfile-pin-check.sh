#!/usr/bin/env bash
# Fails if any Dockerfile FROM line is unpinned (no @sha256 digest).
# Fix with `make pin-base-images`.
set -euo pipefail

unpinned=$(grep -E '^FROM ' Dockerfile | grep -v '@sha256:' || true)
if [ -n "$unpinned" ]; then
  echo "Unpinned FROM line(s) found — pin to @sha256 digest (see 'make pin-base-images')"
  echo "$unpinned"
  exit 1
fi
