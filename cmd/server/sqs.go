package main

import (
	"context"
	"encoding/json"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/pkg/events"

	sqsadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/sqs"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func newSQSHandler(
	log port.Logger,
	versionSvc *service.VersionService,
) func(context.Context, events.Envelope[json.RawMessage]) error {
	return sqsadapter.NewHandler(log, versionSvc)
}
