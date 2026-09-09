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
