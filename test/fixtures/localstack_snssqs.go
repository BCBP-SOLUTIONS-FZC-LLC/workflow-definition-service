//go:build integration

package fixtures

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/glue"
	gluetypes "github.com/aws/aws-sdk-go-v2/service/glue/types"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Queue and registry names match the fan-out topology in api/asyncapi.yaml and
// scripts/localstack-init.sh.
const (
	WFTopicName        = "wf.template.events"
	MembershipQueue    = "membership-wf-q"
	MembershipDLQ      = "membership-wf-q-dlq"
	GlueRegistryName   = "workflow-template-events"
	GlueSchemaName     = "WorkflowTemplatePublished"
)

// LocalStackSNSSQS holds a running LocalStack container pre-configured with:
//   - wf.template.events SNS topic
//   - membership-wf-q SQS queue with its DLQ (membership-wf-q-dlq, redrive maxReceiveCount=5)
//   - SNS subscription with RawMessageDelivery=true
//   - workflow-template-events Glue registry with WorkflowTemplatePublished schema
type LocalStackSNSSQS struct {
	Container        testcontainers.Container
	EndpointURL      string
	SNSClient        *sns.Client
	SQSClient        *sqs.Client
	GlueClient       *glue.Client
	TopicARN         string
	MembershipQueue  string
}

// NewLocalStackSNSSQS starts a LocalStack 3 container with SNS, SQS, and Glue,
// and creates all resources matching the production topology.
// The container is terminated automatically on t.Cleanup.
func NewLocalStackSNSSQS(ctx context.Context, t *testing.T) *LocalStackSNSSQS {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test — requires Docker (-short skips)")
	}

	req := testcontainers.ContainerRequest{
		Image:        "localstack/localstack:3",
		ExposedPorts: []string{"4566/tcp"},
		Env:          map[string]string{"SERVICES": "sns,sqs,glue"},
		WaitingFor: wait.ForLog("Ready.").
			WithOccurrence(1).
			WithStartupTimeout(90 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("NewLocalStackSNSSQS: start container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("NewLocalStackSNSSQS: host: %v", err)
	}
	mappedPort, err := container.MappedPort(ctx, "4566")
	if err != nil {
		t.Fatalf("NewLocalStackSNSSQS: port: %v", err)
	}
	endpointURL := fmt.Sprintf("http://%s:%s", host, mappedPort.Port())

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("test", "test", ""),
		),
	)
	if err != nil {
		t.Fatalf("NewLocalStackSNSSQS: aws config: %v", err)
	}

	snsClient := sns.NewFromConfig(awsCfg, func(o *sns.Options) {
		o.BaseEndpoint = aws.String(endpointURL)
	})
	sqsClient := sqs.NewFromConfig(awsCfg, func(o *sqs.Options) {
		o.BaseEndpoint = aws.String(endpointURL)
	})
	glueClient := glue.NewFromConfig(awsCfg, func(o *glue.Options) {
		o.BaseEndpoint = aws.String(endpointURL)
	})

	topicOut, err := snsClient.CreateTopic(ctx, &sns.CreateTopicInput{
		Name: aws.String(WFTopicName),
	})
	if err != nil {
		t.Fatalf("NewLocalStackSNSSQS: CreateTopic: %v", err)
	}
	topicARN := aws.ToString(topicOut.TopicArn)

	ls := &LocalStackSNSSQS{
		Container:   container,
		EndpointURL: endpointURL,
		SNSClient:   snsClient,
		SQSClient:   sqsClient,
		GlueClient:  glueClient,
		TopicARN:    topicARN,
	}

	ls.MembershipQueue = createQueueWithDLQAndSubscribe(ctx, t, snsClient, sqsClient, topicARN,
		MembershipQueue, MembershipDLQ)
	ls.createGlueResources(ctx, t)

	return ls
}

// createQueueWithDLQAndSubscribe creates a DLQ, the main queue with a redrive
// policy (maxReceiveCount=5), and subscribes it to the SNS topic with
// RawMessageDelivery=true.
func createQueueWithDLQAndSubscribe(
	ctx context.Context, t *testing.T,
	snsClient *sns.Client, sqsClient *sqs.Client,
	topicARN, queueName, dlqName string,
) string {
	t.Helper()

	dlqOut, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(dlqName),
	})
	if err != nil {
		t.Fatalf("createQueueWithDLQAndSubscribe: CreateQueue %s: %v", dlqName, err)
	}
	dlqAttr, err := sqsClient.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       dlqOut.QueueUrl,
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatalf("createQueueWithDLQAndSubscribe: GetQueueAttributes %s: %v", dlqName, err)
	}
	dlqARN := dlqAttr.Attributes[string(sqstypes.QueueAttributeNameQueueArn)]

	redrivePolicy, _ := json.Marshal(map[string]string{
		"deadLetterTargetArn": dlqARN,
		"maxReceiveCount":     "5",
	})

	queueOut, err := sqsClient.CreateQueue(ctx, &sqs.CreateQueueInput{
		QueueName: aws.String(queueName),
		Attributes: map[string]string{
			"RedrivePolicy": string(redrivePolicy),
		},
	})
	if err != nil {
		t.Fatalf("createQueueWithDLQAndSubscribe: CreateQueue %s: %v", queueName, err)
	}
	queueURL := aws.ToString(queueOut.QueueUrl)

	queueAttr, err := sqsClient.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(queueURL),
		AttributeNames: []sqstypes.QueueAttributeName{sqstypes.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatalf("createQueueWithDLQAndSubscribe: GetQueueAttributes %s: %v", queueName, err)
	}
	queueARN := queueAttr.Attributes[string(sqstypes.QueueAttributeNameQueueArn)]

	_, err = snsClient.Subscribe(ctx, &sns.SubscribeInput{
		TopicArn: aws.String(topicARN),
		Protocol: aws.String("sqs"),
		Endpoint: aws.String(queueARN),
		Attributes: map[string]string{
			"RawMessageDelivery": "true",
		},
	})
	if err != nil {
		t.Fatalf("createQueueWithDLQAndSubscribe: Subscribe %s → %s: %v", queueName, topicARN, err)
	}

	return queueURL
}

// createGlueResources creates the workflow-template-events Glue registry and
// registers the WorkflowTemplatePublished schema — mirroring scripts/localstack-init.sh.
func (ls *LocalStackSNSSQS) createGlueResources(ctx context.Context, t *testing.T) {
	t.Helper()

	if _, err := ls.GlueClient.CreateRegistry(ctx, &glue.CreateRegistryInput{
		RegistryName: aws.String(GlueRegistryName),
	}); err != nil {
		t.Fatalf("createGlueResources: CreateRegistry: %v", err)
	}

	const schemaDef = `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "properties": {
    "workflow_id":              { "type": "string", "format": "uuid" },
    "workflow_key":             { "type": "string" },
    "version_id":               { "type": "string", "format": "uuid" },
    "version_number":           { "type": "integer" },
    "artifact_hash":            { "type": "string" },
    "published_by":             { "type": "string", "format": "uuid" },
    "promoted_from_version_id": { "type": ["string", "null"], "format": "uuid" }
  },
  "required": ["workflow_id", "workflow_key", "version_id", "version_number", "artifact_hash", "published_by"]
}`

	if _, err := ls.GlueClient.CreateSchema(ctx, &glue.CreateSchemaInput{
		RegistryId: &gluetypes.RegistryId{
			RegistryName: aws.String(GlueRegistryName),
		},
		SchemaName:       aws.String(GlueSchemaName),
		DataFormat:       gluetypes.DataFormatJson,
		Compatibility:    gluetypes.CompatibilityBackward,
		SchemaDefinition: aws.String(schemaDef),
	}); err != nil {
		t.Fatalf("createGlueResources: CreateSchema %q: %v", GlueSchemaName, err)
	}
}

// SchemaVersionID returns the Glue schema version UUID for WorkflowTemplatePublished.
// Use this in tests to verify that the registry was populated correctly.
func (ls *LocalStackSNSSQS) SchemaVersionID(ctx context.Context, t *testing.T) string {
	t.Helper()
	out, err := ls.GlueClient.GetSchemaVersion(ctx, &glue.GetSchemaVersionInput{
		SchemaId: &gluetypes.SchemaId{
			SchemaName:   aws.String(GlueSchemaName),
			RegistryName: aws.String(GlueRegistryName),
		},
		SchemaVersionNumber: &gluetypes.SchemaVersionNumber{
			LatestVersion: true,
		},
	})
	if err != nil {
		t.Fatalf("SchemaVersionID: %v", err)
	}
	return aws.ToString(out.SchemaVersionId)
}

// DrainQueue receives and deletes all currently available messages from the
// given SQS queue URL, returning each message body. Uses short-poll.
func (ls *LocalStackSNSSQS) DrainQueue(ctx context.Context, t *testing.T, queueURL string) []string {
	t.Helper()
	var bodies []string
	for {
		out, err := ls.SQSClient.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(queueURL),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     0,
		})
		if err != nil {
			t.Fatalf("DrainQueue: ReceiveMessage: %v", err)
		}
		if len(out.Messages) == 0 {
			break
		}
		for _, m := range out.Messages {
			bodies = append(bodies, aws.ToString(m.Body))
			if _, delErr := ls.SQSClient.DeleteMessage(ctx, &sqs.DeleteMessageInput{
				QueueUrl:      aws.String(queueURL),
				ReceiptHandle: m.ReceiptHandle,
			}); delErr != nil {
				t.Logf("DrainQueue: DeleteMessage warning: %v", delErr)
			}
		}
	}
	return bodies
}

// PollQueue polls the given SQS queue URL until at least minCount messages
// arrive or timeout is reached.
func (ls *LocalStackSNSSQS) PollQueue(ctx context.Context, t *testing.T, queueURL string, minCount int, timeout time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var bodies []string
	for time.Now().Before(deadline) {
		bodies = append(bodies, ls.DrainQueue(ctx, t, queueURL)...)
		if len(bodies) >= minCount {
			return bodies
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(bodies) < minCount {
		t.Errorf("PollQueue: got %d message(s), want at least %d after %s", len(bodies), minCount, timeout)
	}
	return bodies
}
