#!/usr/bin/env bash
# Executed automatically by LocalStack when the container is ready (ready.d hook).
# Creates the SNS topic and SQS queue used by the definition service, then
# subscribes the queue to the topic so outbox events flow end-to-end locally.
set -euo pipefail

AWS="awslocal"   # awslocal is pre-installed in localstack/localstack image

echo "[localstack-init] creating SNS topic: wf.template.events"
TOPIC_ARN=$($AWS sns create-topic --name wf.template.events --output text --query TopicArn)

echo "[localstack-init] creating SQS queue: membership-wf-q"
$AWS sqs create-queue --queue-name membership-wf-q
QUEUE_ARN=$($AWS sqs get-queue-attributes \
  --queue-url http://localhost:4566/000000000000/membership-wf-q \
  --attribute-names QueueArn --output text --query 'Attributes.QueueArn')

echo "[localstack-init] subscribing queue to topic"
$AWS sns subscribe --topic-arn "$TOPIC_ARN" --protocol sqs --notification-endpoint "$QUEUE_ARN"

echo "[localstack-init] done. topic=$TOPIC_ARN queue=$QUEUE_ARN"
