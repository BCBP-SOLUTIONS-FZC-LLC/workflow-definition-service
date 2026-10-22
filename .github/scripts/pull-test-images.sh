#!/usr/bin/env bash
# Restores testcontainers images from a tarball cache (docker-cache-hit=true)
# or pulls + saves them fresh for next run's cache.
set -euo pipefail

if [ "${DOCKER_CACHE_HIT:-false}" = "true" ]; then
  docker load < /tmp/docker-images.tar.gz
else
  docker pull postgres:18-alpine
  docker pull valkey/valkey:8-alpine
  docker save postgres:18-alpine valkey/valkey:8-alpine \
    | gzip > /tmp/docker-images.tar.gz
fi
