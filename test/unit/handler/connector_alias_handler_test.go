package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func aliasRouter(h *handler.Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/internal/connector-aliases", h.ListConnectorAliases)
	r.POST("/internal/connector-aliases/rest", h.WriteConnectorRestAlias)
	r.DELETE("/internal/connector-aliases/rest/:alias", h.DeleteConnectorRestAlias)
	return r
}

func TestListConnectorAliases(t *testing.T) {
	conn := &fakeConnectorSvc{
		listAliasesFn: func(_ context.Context) ([]domain.ConnectorRestAlias, error) {
			return []domain.ConnectorRestAlias{{Alias: "tender-get", Method: "GET", BaseURL: "http://tender-service.internal", PathTemplate: "/x", Timeout: 5 * time.Second}}, nil
		},
	}
	w := do(aliasRouter(handler.New(handler.Services{Connectors: conn})), req(http.MethodGet, "/internal/connector-aliases", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Len(t, resp["restCall"], 1)
}

func TestWriteConnectorRestAlias_Success(t *testing.T) {
	var got domain.ConnectorRestAlias
	conn := &fakeConnectorSvc{
		writeRestFn: func(_ context.Context, a domain.ConnectorRestAlias) error {
			got = a
			return nil
		},
	}
	body := map[string]any{"alias": "tender-get", "method": "GET", "baseURL": "http://tender-service.internal", "pathTemplate": "/x"}
	w := do(aliasRouter(handler.New(handler.Services{Connectors: conn})), req(http.MethodPost, "/internal/connector-aliases/rest", body))
	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "tender-get", got.Alias)
	assert.Equal(t, 5*time.Second, got.Timeout)
}

func TestWriteConnectorRestAlias_ValidationError(t *testing.T) {
	conn := &fakeConnectorSvc{
		writeRestFn: func(_ context.Context, a domain.ConnectorRestAlias) error {
			return a.Validate()
		},
	}
	body := map[string]any{"alias": "tender-get", "method": "NOPE", "baseURL": "http://x", "pathTemplate": "/x"}
	w := do(aliasRouter(handler.New(handler.Services{Connectors: conn})), req(http.MethodPost, "/internal/connector-aliases/rest", body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDeleteConnectorRestAlias_NotFound(t *testing.T) {
	conn := &fakeConnectorSvc{
		deleteRestFn: func(context.Context, string) (bool, error) { return false, nil },
	}
	w := do(aliasRouter(handler.New(handler.Services{Connectors: conn})), req(http.MethodDelete, "/internal/connector-aliases/rest/missing", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestDeleteConnectorRestAlias_Success(t *testing.T) {
	conn := &fakeConnectorSvc{
		deleteRestFn: func(context.Context, string) (bool, error) { return true, nil },
	}
	w := do(aliasRouter(handler.New(handler.Services{Connectors: conn})), req(http.MethodDelete, "/internal/connector-aliases/rest/tender-get", nil))
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestWriteConnectorRestAlias_BindError(t *testing.T) {
	conn := &fakeConnectorSvc{}
	w := do(aliasRouter(handler.New(handler.Services{Connectors: conn})), req(http.MethodPost, "/internal/connector-aliases/rest", map[string]any{"alias": "x"}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListConnectorAliases_Error(t *testing.T) {
	conn := &fakeConnectorSvc{
		listAliasesFn: func(context.Context) ([]domain.ConnectorRestAlias, error) {
			return nil, domain.ErrUpstreamUnavailable
		},
	}
	w := do(aliasRouter(handler.New(handler.Services{Connectors: conn})), req(http.MethodGet, "/internal/connector-aliases", nil))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}
