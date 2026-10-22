#!/usr/bin/env bash
# Fails when api/internal-openapi.yaml or docs/swagger/openapi.yaml makes a
# breaking change against its version on CONTRACT_BASE_REF (default
# origin/main). A spec not on the base ref yet passes as new; a base ref that
# does not exist fails. OASDIFF overrides the oasdiff binary.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
specs=(api/internal-openapi.yaml docs/swagger/openapi.yaml)
base_ref="${CONTRACT_BASE_REF:-origin/main}"
oasdiff="${OASDIFF:-$root/.tools/oasdiff}"

if ! git -C "$root" rev-parse --verify --quiet "$base_ref^{commit}" >/dev/null; then
  echo "✗ $base_ref is not a ref here; fetch it first" >&2
  exit 1
fi
base="$(mktemp)"
trap 'rm -f "$base"' EXIT
for spec in "${specs[@]}"; do
  if ! git -C "$root" show "$base_ref:$spec" >"$base" 2>/dev/null; then
    echo "… $spec is not on $base_ref yet: a new contract, nothing to break"
    continue
  fi
  "$oasdiff" breaking "$base" "$root/$spec" --fail-on ERR
  echo "✓ no breaking change to $spec against $base_ref"
done
