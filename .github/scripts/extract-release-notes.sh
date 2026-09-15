#!/usr/bin/env bash
# Extracts the CHANGELOG.md section for the current tag (github.ref_name,
# e.g. v1.2.3) into release-notes.md for use as GitHub Release notes.
set -euo pipefail

version="${REF_NAME#v}"

awk "/^## \\[${version}\\]/,/^## \\[/" CHANGELOG.md \
  | head -n -1 \
  | tail -n +2 \
  > release-notes.md

if [ ! -s release-notes.md ]; then
  echo "No CHANGELOG entry found for ${version} — using tag message as notes"
fi
