#!/usr/bin/env bash
# Installs go-arch-lint (if not already present) and enforces the Clean
# Architecture import direction rules declared in .go-arch-lint.yml.
set -euo pipefail

GOARCHLINT_VERSION="v1.4.0"

if ! command -v go-arch-lint &>/dev/null; then
  echo "installing go-arch-lint ${GOARCHLINT_VERSION}"
  go install "github.com/fe3dback/go-arch-lint@${GOARCHLINT_VERSION}"
fi

go-arch-lint check --project-path .
