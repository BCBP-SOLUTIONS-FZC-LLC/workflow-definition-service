//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

const w8cCallerBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" xmlns:bpmndi="http://www.omg.org/spec/BPMN/20100524/DI" id="Def_W8c" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="Process_W8cMain" name="Main" isExecutable="true">
    <bpmn:laneSet id="LaneSet_1">
      <bpmn:lane id="Lane_eng" name="Engineering"><bpmn:extensionElements><zeebe:properties><zeebe:property name="dept_id" value="2200a5ae-2466-54f7-a1d8-243106e05705"/></zeebe:properties></bpmn:extensionElements>
        <bpmn:flowNodeRef>Start_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>CA_1</bpmn:flowNodeRef>
        <bpmn:flowNodeRef>End_1</bpmn:flowNodeRef>
      </bpmn:lane>
    </bpmn:laneSet>
    <bpmn:startEvent id="Start_1"/>
    <bpmn:callActivity id="CA_1" name="Review">
      <bpmn:extensionElements><zeebe:calledElement processId="%PROCESS%"/></bpmn:extensionElements>
    </bpmn:callActivity>
    <bpmn:endEvent id="End_1"/>
    <bpmn:sequenceFlow id="Flow_1" sourceRef="Start_1" targetRef="CA_1"/>
    <bpmn:sequenceFlow id="Flow_2" sourceRef="CA_1" targetRef="End_1"/>
  </bpmn:process>
  <bpmndi:BPMNDiagram id="BPMNDiagram_1">
    <bpmndi:BPMNPlane id="BPMNPlane_1" bpmnElement="Process_W8cMain">
      <bpmndi:BPMNShape id="Lane_eng_di" bpmnElement="Lane_eng"/>
      <bpmndi:BPMNShape id="Start_1_di" bpmnElement="Start_1"/>
      <bpmndi:BPMNShape id="CA_1_di" bpmnElement="CA_1"/>
      <bpmndi:BPMNShape id="End_1_di" bpmnElement="End_1"/>
      <bpmndi:BPMNEdge id="Flow_1_di" bpmnElement="Flow_1"/>
      <bpmndi:BPMNEdge id="Flow_2_di" bpmnElement="Flow_2"/>
    </bpmndi:BPMNPlane>
  </bpmndi:BPMNDiagram>
</bpmn:definitions>`

const w8cModuleBPMN = `<?xml version="1.0" encoding="UTF-8"?>
<bpmn:definitions xmlns:bpmn="http://www.omg.org/spec/BPMN/20100524/MODEL" xmlns:zeebe="http://camunda.org/schema/zeebe/1.0" id="Def_W8cModule" targetNamespace="http://bpmn.io/schema/bpmn">
  <bpmn:process id="%PROCESS%" name="Review" isExecutable="true">
    <bpmn:startEvent id="Review_Start"/>
    <bpmn:userTask id="Review_Task" name="Review">
      <bpmn:extensionElements>
        <zeebe:taskDefinition type="review"/>
        <zeebe:assignmentDefinition candidateGroups="reviewer" candidateUsers="018f2d3c-0000-7000-8000-0000000000a1"/>
      </bpmn:extensionElements>
    </bpmn:userTask>
    <bpmn:endEvent id="Review_End"/>
    <bpmn:sequenceFlow id="Review_F1" sourceRef="Review_Start" targetRef="Review_Task"/>
    <bpmn:sequenceFlow id="Review_F2" sourceRef="Review_Task" targetRef="Review_End"/>
  </bpmn:process>
</bpmn:definitions>`

func w8cProcessID() string {
	return "Process_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

func w8cSeedPublishedModule(t *testing.T, pool *pgcommon.Pool, owner *uuid.UUID, processID string) {
	t.Helper()
	ctx := context.Background()
	scope := domain.ScopeTenant
	if owner == nil {
		scope = domain.ScopeGlobal
	}
	m := &domain.Module{ID: uuid.New(), TenantID: owner, Scope: scope, Name: processID, ProcessID: processID}
	require.NoError(t, postgres.NewModuleRepo(pool).Create(ctx, m))
	v := &domain.ModuleVersion{
		ID: uuid.New(), ModuleID: m.ID, TenantID: owner, Scope: scope, Status: domain.VersionStatusDraft,
		BPMNXML: strings.ReplaceAll(w8cModuleBPMN, "%PROCESS%", processID), ProcessID: processID, IsValid: true,
	}
	require.NoError(t, postgres.NewModuleVersionRepo(pool).Create(ctx, v))
	require.NoError(t, postgres.NewModuleVersionRepo(pool).Publish(ctx, callerTenant(owner), v.ID, 1))
	require.NoError(t, postgres.NewModuleRepo(pool).UpdateActiveVersion(ctx, m.ID, &v.ID))
}

type w8cValidateResult struct {
	status  int
	isValid bool
	issues  []map[string]any
}

func w8cValidateAs(t *testing.T, pool *pgcommon.Pool, tenantID, userID uuid.UUID, calledProcess string) w8cValidateResult {
	t.Helper()
	body, err := json.Marshal(map[string]string{"bpmn_xml": strings.ReplaceAll(w8cCallerBPMN, "%PROCESS%", calledProcess)})
	require.NoError(t, err)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/workflows/validate", strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("x-tenant-id", tenantID.String())
	r.Header.Set("x-user-id", userID.String())
	r.Header.Set("x-tenant-roles", "tenant_admin")
	w := httptest.NewRecorder()
	w7cRouter(pool).ServeHTTP(w, r)
	var resp struct {
		IsValid bool             `json:"is_valid"`
		Issues  []map[string]any `json:"issues"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp), w.Body.String())
	return w8cValidateResult{status: w.Code, isValid: resp.IsValid, issues: resp.Issues}
}

func TestValidateBPMN_CallToAModuleOfAnotherTenant_IsUnresolvedLikeAMissingModuleAndGlobalModulesAreVisible(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)

	t.Run("a tenant module of another tenant", func(t *testing.T) {
		tenantA, userA, tenantB, userB := uuid.New(), uuid.New(), uuid.New(), uuid.New()
		poolA := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantA, &userA)
		poolB := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantB, &userB)
		foreign := w8cProcessID()
		w8cSeedPublishedModule(t, superPool, &tenantB, foreign)

		asOther := w8cValidateAs(t, poolA, tenantA, userA, foreign)
		asMissing := w8cValidateAs(t, poolA, tenantA, userA, w8cProcessID())
		asOwner := w8cValidateAs(t, poolB, tenantB, userB, foreign)

		require.Equal(t, http.StatusOK, asOther.status, "another tenant's module is not a 403 or 404")
		assert.False(t, asOther.isValid)
		require.Len(t, asOther.issues, 1)
		assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", asOther.issues[0]["code"])
		assert.Equal(t, "CA_1", asOther.issues[0]["node_id"])
		assert.Equal(t, string(domain.SeverityError), asOther.issues[0]["severity"])

		require.Equal(t, http.StatusOK, asMissing.status)
		assert.False(t, asMissing.isValid)
		require.Len(t, asMissing.issues, 1)
		assert.Equal(t, asMissing.issues[0]["code"], asOther.issues[0]["code"], "a foreign module looks like a missing one")
		assert.Equal(t, asMissing.issues[0]["node_id"], asOther.issues[0]["node_id"])
		assert.Equal(t, asMissing.issues[0]["severity"], asOther.issues[0]["severity"])

		require.Equal(t, http.StatusOK, asOwner.status)
		assert.True(t, asOwner.isValid, "the owning tenant resolves the same call: %v", asOwner.issues)
		assert.Empty(t, asOwner.issues)
	})

	t.Run("a tenant module of another tenant named by the owner's tenant id is stopped by row-level security", func(t *testing.T) {
		tenantA, userA, tenantB := uuid.New(), uuid.New(), uuid.New()
		poolA := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantA, &userA)
		foreign := w8cProcessID()
		w8cSeedPublishedModule(t, superPool, &tenantB, foreign)

		got := w8cValidateAs(t, poolA, tenantB, userA, foreign)

		require.Equal(t, http.StatusOK, got.status)
		assert.False(t, got.isValid, "the owner's module does not resolve through the other tenant's connection")
		require.Len(t, got.issues, 1)
		assert.Equal(t, "UNRESOLVED_CALLED_ELEMENT", got.issues[0]["code"])
		assert.Equal(t, "CA_1", got.issues[0]["node_id"])
	})

	t.Run("a global module", func(t *testing.T) {
		tenantA, userA := uuid.New(), uuid.New()
		poolA := fixtures.NewRestrictedRolePool(t, dsn, false, &tenantA, &userA)
		global := w8cProcessID()
		w8cSeedPublishedModule(t, superPool, nil, global)

		got := w8cValidateAs(t, poolA, tenantA, userA, global)

		require.Equal(t, http.StatusOK, got.status)
		assert.True(t, got.isValid, "a global module is visible to every tenant: %v", got.issues)
		assert.Empty(t, got.issues)
	})
}
