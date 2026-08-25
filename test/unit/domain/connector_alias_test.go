package domain_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func validRestAlias() domain.ConnectorRestAlias {
	return domain.ConnectorRestAlias{
		Alias: "tender-get", Method: "GET",
		BaseURL: "http://tender-service.internal", PathTemplate: "/x",
	}
}

func TestConnectorRestAlias_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*domain.ConnectorRestAlias)
		wantErr bool
	}{
		{"valid", func(*domain.ConnectorRestAlias) {}, false},
		{"missing alias", func(a *domain.ConnectorRestAlias) { a.Alias = "" }, true},
		{"invalid method", func(a *domain.ConnectorRestAlias) { a.Method = "NOPE" }, true},
		{"lowercase method valid", func(a *domain.ConnectorRestAlias) { a.Method = "get" }, false},
		{"missing baseURL", func(a *domain.ConnectorRestAlias) { a.BaseURL = "" }, true},
		{"missing pathTemplate", func(a *domain.ConnectorRestAlias) { a.PathTemplate = "" }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := validRestAlias()
			tt.mutate(&a)
			err := a.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.True(t, errors.Is(err, domain.ErrInvalidConnectorAliasInput))
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func validSQLAlias() domain.ConnectorSQLAlias {
	return domain.ConnectorSQLAlias{
		Alias: "get-active-tenants", BaseURL: "http://tender-service.internal",
		Path: "/x", QueryID: "q1", ParamCount: 1,
	}
}

func TestConnectorSQLAlias_Validate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*domain.ConnectorSQLAlias)
		wantErr bool
	}{
		{"valid", func(*domain.ConnectorSQLAlias) {}, false},
		{"missing alias", func(q *domain.ConnectorSQLAlias) { q.Alias = "" }, true},
		{"missing baseURL", func(q *domain.ConnectorSQLAlias) { q.BaseURL = "" }, true},
		{"missing path", func(q *domain.ConnectorSQLAlias) { q.Path = "" }, true},
		{"missing queryId", func(q *domain.ConnectorSQLAlias) { q.QueryID = "" }, true},
		{"negative paramCount", func(q *domain.ConnectorSQLAlias) { q.ParamCount = -1 }, true},
		{"zero paramCount valid", func(q *domain.ConnectorSQLAlias) { q.ParamCount = 0 }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := validSQLAlias()
			tt.mutate(&q)
			err := q.Validate()
			if tt.wantErr {
				assert.Error(t, err)
				assert.True(t, errors.Is(err, domain.ErrInvalidConnectorAliasInput))
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
