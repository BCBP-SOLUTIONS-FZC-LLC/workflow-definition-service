package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

type fakeSecretsClient struct {
	writeFn func(ctx context.Context, path string, data map[string]string) error
	written []string
}

func (f *fakeSecretsClient) Write(ctx context.Context, path string, data map[string]string) error {
	f.written = append(f.written, path)
	if f.writeFn != nil {
		return f.writeFn(ctx, path, data)
	}
	return nil
}

func TestConnectorService_WriteCredential_Success(t *testing.T) {
	secrets := &fakeSecretsClient{}
	svc := service.NewConnectorService(service.ConnectorDeps{Secrets: secrets})

	path, err := svc.WriteCredential(context.Background(), uuid.New(), "send-email", "apiKey", "sg-live-abc")
	require.NoError(t, err)
	assert.Contains(t, path, "send-email/apiKey")
	assert.Len(t, secrets.written, 1)
}

// TestConnectorService_WriteCredential_PathTraversal is the regression test
// for the critical finding: connectorType/fieldName were interpolated
// straight into the OpenBao path with no validation, letting a "../" segment
// climb out of the caller's own tenant subtree. Neither value should ever
// reach the SecretsClient once rejected.
func TestConnectorService_WriteCredential_PathTraversal(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		connectorType string
		fieldName     string
	}{
		{"traversal in field_name", "send-email", "../other-tenant/apiKey"},
		{"slash in field_name", "send-email", "a/b"},
		{"traversal in connector_type", "../other-tenant", "apiKey"},
		{"unregistered connector_type", "not-a-real-connector", "apiKey"},
		{"empty connector_type", "", "apiKey"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			secrets := &fakeSecretsClient{}
			svc := service.NewConnectorService(service.ConnectorDeps{Secrets: secrets})

			_, err := svc.WriteCredential(context.Background(), uuid.New(), tc.connectorType, tc.fieldName, "secret-value")
			require.Error(t, err)
			assert.True(t, errors.Is(err, domain.ErrInvalidConnectorCredentialInput), "got: %v", err)
			assert.Empty(t, secrets.written, "rejected input must never reach SecretsClient.Write")
		})
	}
}

func TestConnectorService_WriteCredential_UpstreamUnavailable(t *testing.T) {
	secrets := &fakeSecretsClient{
		writeFn: func(context.Context, string, map[string]string) error {
			return errors.New("connection refused: dial tcp 10.0.0.5:8200")
		},
	}
	svc := service.NewConnectorService(service.ConnectorDeps{Secrets: secrets})

	_, err := svc.WriteCredential(context.Background(), uuid.New(), "send-email", "apiKey", "x")
	require.Error(t, err)
}

type fakeAliasRepo struct {
	rest       []domain.ConnectorRestAlias
	sql        []domain.ConnectorSQLAlias
	upserts    []domain.ConnectorRestAlias
	sqlUpserts []domain.ConnectorSQLAlias
	deleted    []string
	sqlDeleted []string
}

func (f *fakeAliasRepo) ListRest(context.Context) ([]domain.ConnectorRestAlias, error) {
	return f.rest, nil
}
func (f *fakeAliasRepo) ListSQL(context.Context) ([]domain.ConnectorSQLAlias, error) {
	return f.sql, nil
}
func (f *fakeAliasRepo) UpsertRest(_ context.Context, a domain.ConnectorRestAlias) error {
	f.upserts = append(f.upserts, a)
	return nil
}
func (f *fakeAliasRepo) UpsertSQL(_ context.Context, q domain.ConnectorSQLAlias) error {
	f.sqlUpserts = append(f.sqlUpserts, q)
	return nil
}
func (f *fakeAliasRepo) DeleteRest(_ context.Context, alias string) (bool, error) {
	f.deleted = append(f.deleted, alias)
	return true, nil
}
func (f *fakeAliasRepo) DeleteSQL(_ context.Context, alias string) (bool, error) {
	f.sqlDeleted = append(f.sqlDeleted, alias)
	return true, nil
}

func TestConnectorService_WriteRestAlias_RejectsInvalidMethod(t *testing.T) {
	repo := &fakeAliasRepo{}
	svc := service.NewConnectorService(service.ConnectorDeps{Aliases: repo})

	err := svc.WriteRestAlias(context.Background(), domain.ConnectorRestAlias{
		Alias: "x", Method: "NOPE", BaseURL: "http://x", PathTemplate: "/y",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInvalidConnectorAliasInput))
	assert.Empty(t, repo.upserts, "rejected input must never reach the repository")
}

func TestConnectorService_WriteRestAlias_Success(t *testing.T) {
	repo := &fakeAliasRepo{}
	svc := service.NewConnectorService(service.ConnectorDeps{Aliases: repo})

	err := svc.WriteRestAlias(context.Background(), domain.ConnectorRestAlias{
		Alias: "tender-get", Method: "GET", BaseURL: "http://tender-service.internal", PathTemplate: "/x",
	})
	require.NoError(t, err)
	require.Len(t, repo.upserts, 1)
	assert.Equal(t, "tender-get", repo.upserts[0].Alias)
}

func TestConnectorService_ListAliases(t *testing.T) {
	repo := &fakeAliasRepo{
		rest: []domain.ConnectorRestAlias{{Alias: "a"}},
		sql:  []domain.ConnectorSQLAlias{{Alias: "b"}},
	}
	svc := service.NewConnectorService(service.ConnectorDeps{Aliases: repo})

	rest, sql, err := svc.ListAliases(context.Background())
	require.NoError(t, err)
	assert.Len(t, rest, 1)
	assert.Len(t, sql, 1)
}

func TestConnectorService_DeleteRestAlias(t *testing.T) {
	repo := &fakeAliasRepo{}
	svc := service.NewConnectorService(service.ConnectorDeps{Aliases: repo})

	deleted, err := svc.DeleteRestAlias(context.Background(), "tender-get")
	require.NoError(t, err)
	assert.True(t, deleted)
	assert.Equal(t, []string{"tender-get"}, repo.deleted)
}

func TestConnectorService_WriteSQLAlias_RejectsMissingQueryID(t *testing.T) {
	repo := &fakeAliasRepo{}
	svc := service.NewConnectorService(service.ConnectorDeps{Aliases: repo})

	err := svc.WriteSQLAlias(context.Background(), domain.ConnectorSQLAlias{
		Alias: "x", BaseURL: "http://x", Path: "/y",
	})
	require.Error(t, err)
	assert.True(t, errors.Is(err, domain.ErrInvalidConnectorAliasInput))
	assert.Empty(t, repo.sqlUpserts)
}

func TestConnectorService_WriteSQLAlias_Success(t *testing.T) {
	repo := &fakeAliasRepo{}
	svc := service.NewConnectorService(service.ConnectorDeps{Aliases: repo})

	err := svc.WriteSQLAlias(context.Background(), domain.ConnectorSQLAlias{
		Alias: "get-active-tenants", BaseURL: "http://tender-service.internal", Path: "/q", QueryID: "q1",
	})
	require.NoError(t, err)
	require.Len(t, repo.sqlUpserts, 1)
}

func TestConnectorService_DeleteSQLAlias(t *testing.T) {
	repo := &fakeAliasRepo{}
	svc := service.NewConnectorService(service.ConnectorDeps{Aliases: repo})

	deleted, err := svc.DeleteSQLAlias(context.Background(), "get-active-tenants")
	require.NoError(t, err)
	assert.True(t, deleted)
	assert.Equal(t, []string{"get-active-tenants"}, repo.sqlDeleted)
}

func TestConnectorService_Registry(t *testing.T) {
	svc := service.NewConnectorService(service.ConnectorDeps{})
	defs := svc.Registry(context.Background())
	assert.NotEmpty(t, defs)
}
