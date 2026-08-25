package handler

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-connectors/pkg/registry"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

type connectorSvc interface {
	Registry(ctx context.Context) map[string]registry.Definition
	WriteCredential(ctx context.Context, tenantID uuid.UUID, connectorType, fieldName, value string) (string, error)
	ListAliases(ctx context.Context) ([]domain.ConnectorRestAlias, []domain.ConnectorSQLAlias, error)
	WriteRestAlias(ctx context.Context, a domain.ConnectorRestAlias) error
	WriteSQLAlias(ctx context.Context, q domain.ConnectorSQLAlias) error
	DeleteRestAlias(ctx context.Context, alias string) (bool, error)
	DeleteSQLAlias(ctx context.Context, alias string) (bool, error)
}

type connectorFieldResp struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Required    bool     `json:"required"`
	EnumValues  []string `json:"enum_values,omitempty"`
	Condition   string   `json:"condition,omitempty"`
	Description string   `json:"description,omitempty"`
}

type connectorDefinitionResp struct {
	Type        string               `json:"type"`
	DisplayName string               `json:"display_name"`
	Description string               `json:"description"`
	Inputs      []connectorFieldResp `json:"inputs"`
	Outputs     []connectorFieldResp `json:"outputs"`
	Retry       string               `json:"retry"`
}

func toConnectorFieldResp(f registry.Field) connectorFieldResp {
	return connectorFieldResp{
		Name:        f.Name,
		Kind:        string(f.Kind),
		Required:    f.Required,
		EnumValues:  f.EnumValues,
		Condition:   f.Condition,
		Description: f.Description,
	}
}

func toConnectorDefinitionResp(d registry.Definition) connectorDefinitionResp {
	inputs := make([]connectorFieldResp, len(d.Inputs))
	for i, f := range d.Inputs {
		inputs[i] = toConnectorFieldResp(f)
	}
	outputs := make([]connectorFieldResp, len(d.Outputs))
	for i, f := range d.Outputs {
		outputs[i] = toConnectorFieldResp(f)
	}
	return connectorDefinitionResp{
		Type:        d.Type,
		DisplayName: d.DisplayName,
		Description: d.Description,
		Inputs:      inputs,
		Outputs:     outputs,
		Retry:       string(d.Retry),
	}
}

func (h *Handler) ListConnectorRegistry(c *gin.Context) {
	defs := h.connectors.Registry(c.Request.Context())
	resp := make(map[string]connectorDefinitionResp, len(defs))
	for t, d := range defs {
		resp[t] = toConnectorDefinitionResp(d)
	}
	c.JSON(http.StatusOK, gin.H{"connectors": resp})
}

type writeConnectorCredentialReq struct {
	ConnectorType string `json:"connector_type" binding:"required"`
	FieldName     string `json:"field_name" binding:"required"`
	Value         string `json:"value" binding:"required"`
}

func (h *Handler) WriteConnectorCredential(c *gin.Context) {
	tenantID, _, ok := mustCtx(c)
	if !ok {
		return
	}
	if !requireAdmin(c) {
		return
	}

	var req writeConnectorCredentialReq
	if err := c.ShouldBindJSON(&req); err != nil {
		bindErrResponse(c, err)
		return
	}

	secretPath, err := h.connectors.WriteCredential(c.Request.Context(), tenantID, req.ConnectorType, req.FieldName, req.Value)
	if err != nil {
		errResponse(c, h.log, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"secret_path": secretPath})
}
