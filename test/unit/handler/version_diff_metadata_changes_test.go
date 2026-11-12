package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
)

func diffRouterReturning(changes service.DiffChanges, changeType service.ChangeType) func(t *testing.T) *json.RawMessage {
	return func(t *testing.T) *json.RawMessage {
		t.Helper()
		versions := &fakeVersionSvc{
			diff: func(_ context.Context, _, workflowID, baseID, targetID uuid.UUID) (*service.DiffResult, error) {
				return &service.DiffResult{
					WorkflowID: workflowID, BaseVersionID: baseID, TargetVersionID: targetID,
					ChangeType: changeType, Changes: changes,
				}, nil
			},
		}
		router := newRouter(newHandler(&fakeWorkflowSvc{}, &fakeDraftSvc{}, versions, &fakeValidationSvc{}))
		w := do(router, req(http.MethodGet, "/api/v1/workflows/"+testWFID.String()+"/versions/"+testVerID.String()+"/diff/"+testVerID2.String(), nil))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())
		raw := json.RawMessage(w.Body.Bytes())
		return &raw
	}
}

func TestGetVersionDiff_MetadataChanges_AreListedApartFromStageChanges(t *testing.T) {
	raw := diffRouterReturning(service.DiffChanges{
		StageChanges:    []service.StageChange{{NodeKey: "a/T1", Description: "role changed"}},
		MetadataChanges: []service.StageChange{{NodeKey: "a/T1", Description: "due date changed"}},
	}, service.ChangeTypeStructural)(t)

	var resp struct {
		ChangeType string `json:"change_type"`
		Changes    struct {
			StageChanges    []map[string]string `json:"stage_changes"`
			MetadataChanges []map[string]string `json:"metadata_changes"`
			MetadataOnly    bool                `json:"metadata_only"`
		} `json:"changes"`
	}
	require.NoError(t, json.Unmarshal(*raw, &resp))
	assert.Equal(t, "STRUCTURAL", resp.ChangeType)
	assert.False(t, resp.Changes.MetadataOnly)
	assert.Equal(t, []map[string]string{{"node_key": "a/T1", "description": "role changed"}}, resp.Changes.StageChanges)
	assert.Equal(t, []map[string]string{{"node_key": "a/T1", "description": "due date changed"}}, resp.Changes.MetadataChanges)
}

func TestGetVersionDiff_NoMetadataChanges_IsAnEmptyArrayNotNull(t *testing.T) {
	raw := diffRouterReturning(service.DiffChanges{MetadataOnly: true}, service.ChangeTypeMetadataOnly)(t)

	var resp struct {
		Changes map[string]json.RawMessage `json:"changes"`
	}
	require.NoError(t, json.Unmarshal(*raw, &resp))
	assert.Equal(t, "[]", string(resp.Changes["metadata_changes"]))
}
