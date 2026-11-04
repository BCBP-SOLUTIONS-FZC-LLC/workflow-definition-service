#!/usr/bin/env bash
# Fails if any Go file is not gofmt-formatted.
set -euo pipefail

files=$(gofmt -l .)
if [ -n "$files" ]; then
  echo "gofmt violations:"
  echo "$files"
  exit 1
fi
