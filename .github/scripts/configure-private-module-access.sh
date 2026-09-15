#!/usr/bin/env bash
# Configures git to authenticate github.com requests with GO_PRIVATE_TOKEN so
# `go mod download`/`go get` can resolve github.com/BCBP-SOLUTIONS-FZC-LLC/*
# private modules. Requires GO_PRIVATE_TOKEN in the environment.
set -euo pipefail

git config --global credential.helper store
echo "https://x-access-token:${GO_PRIVATE_TOKEN}@github.com" > ~/.git-credentials
chmod 600 ~/.git-credentials
