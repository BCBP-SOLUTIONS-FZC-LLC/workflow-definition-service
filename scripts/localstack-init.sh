#!/usr/bin/env bash
# Executed automatically by LocalStack when the container is ready (ready.d hook).
# Creates the SNS topic, SQS queues (with DLQs), and AWS Glue registry/schema used
# by the definition service. Schema definitions are read from internal/eventschema/
# (mounted read-only at /eventschema) — the same files platform-schemagov validates
# and registers in real Glue via `make schema-register`.
set -euo pipefail

AWS="awslocal"   # awslocal is pre-installed in localstack/localstack image

# subscribe_queue <queue-name> <topic-arn> [filter-policy-json]
#
# Creates a DLQ (<queue-name>-dlq), then the main queue with a redrive policy
# pointing at the DLQ (maxReceiveCount=5), then subscribes the main queue to the
# given SNS topic with RawMessageDelivery=true.  An optional SNS MessageAttribute
# filter policy is applied when the third argument is non-empty.
subscribe_queue() {
  local QUEUE="$1"
  local TOPIC_ARN="$2"
  local FILTER="${3:-}"

  local DLQ="${QUEUE}-dlq"

  echo "[localstack-init] creating DLQ: $DLQ"
  $AWS sqs create-queue --queue-name "$DLQ"
  DLQ_ARN=$($AWS sqs get-queue-attributes \
    --queue-url "http://localhost:4566/000000000000/${DLQ}" \
    --attribute-names QueueArn --output text --query 'Attributes.QueueArn')

  echo "[localstack-init] creating queue: $QUEUE (redrive → $DLQ)"
  REDRIVE=$(printf '{"deadLetterTargetArn":"%s","maxReceiveCount":"5"}' "$DLQ_ARN")
  $AWS sqs create-queue --queue-name "$QUEUE" \
    --attributes "RedrivePolicy=$(echo "$REDRIVE" | sed 's/"/\\"/g')"

  QUEUE_ARN=$($AWS sqs get-queue-attributes \
    --queue-url "http://localhost:4566/000000000000/${QUEUE}" \
    --attribute-names QueueArn --output text --query 'Attributes.QueueArn')

  echo "[localstack-init] subscribing $QUEUE to topic"
  SUB_ARGS=(--topic-arn "$TOPIC_ARN" --protocol sqs --notification-endpoint "$QUEUE_ARN" \
    --attributes RawMessageDelivery=true)

  if [ -n "$FILTER" ]; then
    SUB_ARGS+=(--attributes "FilterPolicy=$(echo "$FILTER" | sed 's/"/\\"/g')")
  fi

  $AWS sns subscribe "${SUB_ARGS[@]}"
}

echo "[localstack-init] creating SNS topic: wf-template-events"
TOPIC_ARN=$($AWS sns create-topic --name wf-template-events --output text --query TopicArn)

echo "[localstack-init] creating AWS Glue Schema Registry: workflow-template-events"
$AWS glue create-registry --registry-name workflow-template-events

echo "[localstack-init] registering schema: WorkflowTemplatePublished"
$AWS glue create-schema \
  --registry-id RegistryName=workflow-template-events \
  --schema-name WorkflowTemplatePublished \
  --data-format JSON \
  --compatibility BACKWARD \
  --schema-definition "file:///eventschema/workflow_template_published.json"

# Fan-out consumers — each gets its own DLQ and an SNS filter on event_type.
# §9.1 Fan-out queue registry (see api/asyncapi.yaml for full topology).
subscribe_queue "membership-wf-q" "$TOPIC_ARN" \
  '{"event_type":["workflow.template.published"]}'

echo "[localstack-init] done. topic=$TOPIC_ARN"
