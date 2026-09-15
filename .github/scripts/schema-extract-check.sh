#!/usr/bin/env bash
# Fails if internal/eventschema/*.json has drifted from api/asyncapi.yaml.
# Flags confirmed against iam-user-profile's working CLAUDE.md/Makefile —
# this CLI has no --workspace flag.
set -euo pipefail

SCHEMA_GOV_IMAGE="${SCHEMA_GOV_IMAGE:-ghcr.io/bcbp-solutions-fzc-llc/platform-schemagov:0.4}"

docker run --rm -v "$(pwd):/workspace" "$SCHEMA_GOV_IMAGE" extract \
  --asyncapi   api/asyncapi.yaml \
  --schema-dir internal/eventschema \
  --check
