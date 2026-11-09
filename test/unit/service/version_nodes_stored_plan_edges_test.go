package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestVersionNodes_CorruptStoredPlan_IsAnInternalErrorNamingTheVersion(t *testing.T) {
	corrupt := "{bad"
	n := int32(1)
	v := &domain.WorkflowVersion{
		ID: uuid.New(), WorkflowID: uuid.New(), TenantID: uuid.New(),
		Status: domain.VersionStatusPublished, VersionNumber: &n, CompiledPlanJSON: &corrupt,
	}

	got, err := nodesService(t, v).Nodes(context.Background(), v.TenantID, v.WorkflowID, v.ID)

	require.Error(t, err)
	assert.Nil(t, got)
	var syntax *json.SyntaxError
	assert.True(t, errors.As(err, &syntax), "the decoder's error is wrapped, got %v", err)
	assert.Contains(t, err.Error(), "version "+v.ID.String()+": decode stored plan")
	for _, domainErr := range []error{
		domain.ErrVersionNotPublished, domain.ErrNotFound, domain.ErrMalformedBPMN, domain.ErrUpstreamUnavailable,
	} {
		assert.NotErrorIs(t, err, domainErr, "a corrupt row must not read as a client-visible condition")
	}
}
