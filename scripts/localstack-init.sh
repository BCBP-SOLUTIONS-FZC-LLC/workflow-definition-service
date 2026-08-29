#!/usr/bin/env bash
# Executed automatically by LocalStack when the container is ready (ready.d hook).
# This service currently defines no outbound events (see api/asyncapi.yaml),
# so there is no SNS topic / Glue registry / schema to seed here. Add the
# topic/registry/schema/subscription setup back when the first outbound
# event is introduced.
set -euo pipefail
