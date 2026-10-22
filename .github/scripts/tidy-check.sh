#!/usr/bin/env bash
# Fails if `go mod tidy` would change go.mod/go.sum (dependency drift).
set -euo pipefail

go mod tidy
git diff --exit-code go.mod go.sum
