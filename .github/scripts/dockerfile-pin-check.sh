#!/usr/bin/env bash
# Fails if any Dockerfile FROM line is unpinned (no @sha256 digest).
# Fix with `make pin-base-images`.
set -euo pipefail

if grep -E '^FROM [^ ]+:[^ @]+( AS .+)?$' Dockerfile; then
  echo "Unpinned FROM line(s) found — pin to @sha256 digest (see 'make pin-base-images')"
  exit 1
fi
