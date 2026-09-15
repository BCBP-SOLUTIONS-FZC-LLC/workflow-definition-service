#!/usr/bin/env bash
# Creates the GitHub Release for the current tag, attaching bin/server and
# using release-notes.md (from extract-release-notes.sh) when non-empty.
# Requires REF_NAME and GH_TOKEN in the environment.
set -euo pipefail

notes_flag="--notes-from-tag"
if [ -s release-notes.md ]; then
  notes_flag="--notes-file release-notes.md"
fi

# shellcheck disable=SC2086
gh release create "$REF_NAME" \
  bin/server \
  --title "$REF_NAME" \
  $notes_flag \
  --verify-tag
