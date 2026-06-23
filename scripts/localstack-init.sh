#!/usr/bin/env bash
# Executed automatically by LocalStack when the container is ready (ready.d hook).
# Creates the SNS topic, SQS queue, and AWS Glue registry/schemas used by the definition service.
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

echo "[localstack-init] creating AWS Glue Schema Registry: workflow-template-events"
$AWS glue create-registry --registry-name workflow-template-events

# Register JSON schemas for the outbound event payloads
SCHEMA_PUB='{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "workflow_id": { "type": "string", "format": "uuid" },
    "version_id": { "type": "string", "format": "uuid" },
    "version_number": { "type": "integer" },
    "published_by": { "type": "string", "format": "uuid" },
    "compiled_plan_json": { "type": "string" },
    "promoted_from_version_id": { "type": ["string", "null"], "format": "uuid" }
  },
  "required": ["workflow_id", "version_id", "version_number", "published_by", "compiled_plan_json"]
}'

SCHEMA_CLONE='{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "source_workflow_id": { "type": "string", "format": "uuid" },
    "source_version_id": { "type": "string", "format": "uuid" },
    "new_workflow_id": { "type": "string", "format": "uuid" },
    "new_workflow_key": { "type": "string" },
    "new_workflow_name": { "type": "string" },
    "cloned_by_user_id": { "type": "string", "format": "uuid" }
  },
  "required": ["source_workflow_id", "source_version_id", "new_workflow_id", "new_workflow_key", "new_workflow_name", "cloned_by_user_id"]
}'

SCHEMA_ARCHIVE='{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "workflow_id": { "type": "string", "format": "uuid" },
    "version_id": { "type": "string", "format": "uuid" },
    "archived_by": { "type": "string", "format": "uuid" }
  },
  "required": ["workflow_id", "version_id", "archived_by"]
}'

SCHEMA_INVALID='{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "workflow_id": { "type": "string", "format": "uuid" },
    "version_id": { "type": "string", "format": "uuid" },
    "version_number": { "type": "integer" },
    "revoked_user_id": { "type": "string", "format": "uuid" },
    "affected_nodes": { "type": "array", "items": { "type": "string" } },
    "reason": { "type": "string" }
  },
  "required": ["workflow_id", "version_id", "version_number", "revoked_user_id", "affected_nodes", "reason"]
}'

echo "[localstack-init] registering schemas..."
$AWS glue create-schema --registry-id RegistryName=workflow-template-events \
  --schema-name WorkflowTemplatePublished --data-format JSON --compatibility BACKWARD --schema-definition "$SCHEMA_PUB"
$AWS glue create-schema --registry-id RegistryName=workflow-template-events \
  --schema-name WorkflowTemplateCloned --data-format JSON --compatibility BACKWARD --schema-definition "$SCHEMA_CLONE"
$AWS glue create-schema --registry-id RegistryName=workflow-template-events \
  --schema-name WorkflowTemplateArchived --data-format JSON --compatibility BACKWARD --schema-definition "$SCHEMA_ARCHIVE"
$AWS glue create-schema --registry-id RegistryName=workflow-template-events \
  --schema-name WorkflowTemplateEligibilityInvalidated --data-format JSON --compatibility BACKWARD --schema-definition "$SCHEMA_INVALID"

echo "[localstack-init] done. topic=$TOPIC_ARN queue=$QUEUE_ARN"
