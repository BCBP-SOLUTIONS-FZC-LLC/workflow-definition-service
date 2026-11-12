//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	httpadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/service"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/fixtures"
)

type w6aTenantSeed struct {
	tenantID  uuid.UUID
	versionID uuid.UUID
}

func w6aSeedAssignedVersion(t *testing.T, pool *pgcommon.Pool, tenantID, userID, departmentID uuid.UUID, nodeKey string) w6aTenantSeed {
	t.Helper()
	ctx := context.Background()
	wf := &domain.Workflow{
		ID: uuid.New(), TenantID: tenantID, CreatedByUserID: uuid.New(),
		TemplateKey: "w6a-" + uuid.NewString(), Name: "Revocation scope",
	}
	require.NoError(t, postgres.NewWorkflowRepo(pool).Create(ctx, wf))
	v := newDraftVersion(wf.ID, tenantID)
	require.NoError(t, postgres.NewWorkflowVersionRepo(pool).Create(ctx, v))
	require.NoError(t, postgres.NewAssigneeRepo(pool).BulkInsert(ctx, tenantID, v.ID, []*domain.NodeAssignee{
		{ID: uuid.New(), NodeKey: nodeKey, UserID: userID, DepartmentID: departmentID, Role: "approver"},
	}))
	return w6aTenantSeed{tenantID: tenantID, versionID: v.ID}
}

func w6aPostRevocation(t *testing.T, appPool *pgcommon.Pool, eventType string, tenantID uuid.UUID, payload string) *httptest.ResponseRecorder {
	t.Helper()
	svc := service.NewVersionService(service.VersionDeps{
		Versions:  postgres.NewWorkflowVersionRepo(appPool),
		Assignees: postgres.NewAssigneeRepo(appPool),
	})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	httpadapter.RegisterInternalRoutes(r.Group("/api/v1/internal"), handler.New(handler.Services{Membership: svc}))
	body := `{"id":"` + uuid.NewString() + `","type":"` + eventType + `","tenant_id":"` + tenantID.String() +
		`","time":"2100-01-01T00:00:00Z","data":` + payload + `}`
	httpReq := httptest.NewRequest(http.MethodPost, "/api/v1/internal/events", bytes.NewReader([]byte(body)))
	httpReq.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httpReq)
	return w
}

func TestMembershipRevocationEvent_OnlyTheAssigneesOfTheEnvelopeTenantAreInvalidated(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	appPool := fixtures.NewContextGUCRolePool(t, dsn)
	ctx := context.Background()
	versions := postgres.NewWorkflowVersionRepo(superPool)

	cases := []struct {
		name      string
		eventType string
		payload   func(userID, departmentID uuid.UUID) string
	}{
		{
			name:      "tenant-wide revocation",
			eventType: "MembershipRevoked",
			payload:   func(userID, _ uuid.UUID) string { return `{"user_id":"` + userID.String() + `"}` },
		},
		{
			name:      "department revocation",
			eventType: "DepartmentMembershipRevoked",
			payload: func(userID, departmentID uuid.UUID) string {
				return `{"user_id":"` + userID.String() + `","department_id":"` + departmentID.String() + `"}`
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			revokedTenant, otherTenant := uuid.New(), uuid.New()
			userID, departmentID := uuid.New(), uuid.New()
			revoked := w6aSeedAssignedVersion(t, superPool, revokedTenant, userID, departmentID, "Task_Revoked")
			other := w6aSeedAssignedVersion(t, superPool, otherTenant, userID, departmentID, "Task_Other")

			w := w6aPostRevocation(t, appPool, tc.eventType, revokedTenant, tc.payload(userID, departmentID))

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			hit, err := versions.GetByID(ctx, revoked.tenantID, revoked.versionID)
			require.NoError(t, err)
			assert.False(t, hit.IsValid, "the version of the envelope tenant is invalidated")
			require.NotNil(t, hit.ValidationErrorsJSON)
			assert.Contains(t, *hit.ValidationErrorsJSON, "Task_Revoked")

			untouched, err := versions.GetByID(ctx, other.tenantID, other.versionID)
			require.NoError(t, err)
			assert.True(t, untouched.IsValid, "the same user's assignment in another tenant is left alone")
			assert.Nil(t, untouched.ValidationErrorsJSON, "no validation errors are recorded for the other tenant's version")
		})
	}
}

func TestAssigneeRepo_ListByUser_TheSameUserInTwoTenants_EachTenantSeesOnlyItsOwnAssignment(t *testing.T) {
	superPool, dsn := fixtures.NewTestPoolAndDSN(t)
	appPool := fixtures.NewContextGUCRolePool(t, dsn)
	tenantA, tenantB := uuid.New(), uuid.New()
	userID, departmentID := uuid.New(), uuid.New()
	a := w6aSeedAssignedVersion(t, superPool, tenantA, userID, departmentID, "Task_A")
	b := w6aSeedAssignedVersion(t, superPool, tenantB, userID, departmentID, "Task_B")
	repo := postgres.NewAssigneeRepo(appPool)

	inTenant := func(tenantID uuid.UUID) context.Context {
		return pgcommon.WithGUCSet(context.Background(), pgdomain.GUCSet{TenantID: tenantID.String()})
	}

	gotA, err := repo.ListByUser(inTenant(tenantA), tenantA, userID)
	require.NoError(t, err)
	require.Len(t, gotA, 1)
	assert.Equal(t, "Task_A", gotA[0].NodeKey)
	assert.Equal(t, a.versionID, gotA[0].WorkflowVersionID)

	gotB, err := repo.ListByUser(inTenant(tenantB), tenantB, userID)
	require.NoError(t, err)
	require.Len(t, gotB, 1)
	assert.Equal(t, "Task_B", gotB[0].NodeKey)
	assert.Equal(t, b.versionID, gotB[0].WorkflowVersionID)

	crossed, err := repo.ListByUser(inTenant(tenantA), tenantB, userID)
	require.NoError(t, err)
	assert.Empty(t, crossed, "a tenant scope cannot read another tenant's assignment")
}
