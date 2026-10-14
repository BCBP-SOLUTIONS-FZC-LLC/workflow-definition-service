package contract_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-connectors/v2/pkg/registry"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/test/contract"
)

type fakeMembership struct{ err error }

func (f fakeMembership) HandleDepartmentMembershipRevoked(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, time.Time) error {
	return f.err
}

func (f fakeMembership) HandleTenantMembershipRevoked(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, time.Time) error {
	return f.err
}

type fakeConnectors struct {
	aliases     []domain.ConnectorRestAlias
	writeErr    error
	listErr     error
	deleteFound bool
	deleteErr   error
}

func (fakeConnectors) Registry(context.Context) map[string]registry.Definition { return nil }

func (fakeConnectors) WriteCredential(context.Context, uuid.UUID, uuid.UUID, string, string, string) (string, error) {
	return "", nil
}

func (fakeConnectors) RevokeCredential(context.Context, uuid.UUID, uuid.UUID, string, string) error {
	return nil
}

func (f fakeConnectors) ListAliases(context.Context) ([]domain.ConnectorRestAlias, error) {
	return f.aliases, f.listErr
}

func (f fakeConnectors) WriteRestAlias(context.Context, domain.ConnectorRestAlias) error {
	return f.writeErr
}

func (f fakeConnectors) DeleteRestAlias(context.Context, string) (bool, error) {
	return f.deleteFound, f.deleteErr
}

func ingestRouter(t *testing.T, membership fakeMembership, connectors fakeConnectors) http.Handler {
	t.Helper()
	return internalRouterWith(t, handler.Services{Membership: membership, Connectors: connectors})
}

const (
	eventsPath  = "/api/v1/internal/events"
	aliasesPath = "/api/v1/internal/connector-aliases"
)

func envelope(typ, tenant, userID, departmentID string) string {
	return `{"id":"` + uuid.NewString() + `","type":"` + typ + `","source":"iam","specversion":"1.0",` +
		`"tenant_id":"` + tenant + `","time":"` + time.Now().UTC().Format(time.RFC3339) + `",` +
		`"data":{"user_id":"` + userID + `","department_id":"` + departmentID + `","role":"reviewer"}}`
}

type exchange struct {
	name, method, path, body, token string
	want                            int
	validReq                        bool
}

func runExchanges(t *testing.T, spec *contract.Spec, router http.Handler, cases []exchange) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body *bytes.Reader
			if tc.body != "" {
				body = bytes.NewReader([]byte(tc.body))
			} else {
				body = bytes.NewReader(nil)
			}
			req := httptest.NewRequest(tc.method, tc.path, body)
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			if tc.token != "" {
				req.Header.Set("x-internal-token", tc.token)
			}
			if tc.validReq {
				spec.Request(t, req)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
			spec.Response(t, req, w.Code, w.Header(), w.Body.Bytes())
		})
	}
}

func TestInternalContract_Events(t *testing.T) {
	spec := contract.Load(t, internalSpec)
	tenant, user, dept := uuid.NewString(), uuid.NewString(), uuid.NewString()
	const token = "internal-secret"
	ok := ingestRouter(t, fakeMembership{}, fakeConnectors{})
	failing := ingestRouter(t, fakeMembership{err: errors.New("db down")}, fakeConnectors{})

	runExchanges(t, spec, ok, []exchange{
		{"department revoked", http.MethodPost, eventsPath, envelope("DepartmentMembershipRevoked", tenant, user, dept), token, http.StatusOK, true},
		{"tenant membership revoked", http.MethodPost, eventsPath, envelope("MembershipRevoked", tenant, user, ""), token, http.StatusOK, true},
		{"unhandled type", http.MethodPost, eventsPath, envelope("UserDeleted", tenant, user, ""), token, http.StatusOK, true},
		{"not json", http.MethodPost, eventsPath, `{`, token, http.StatusBadRequest, false},
		{"bad user id", http.MethodPost, eventsPath, envelope("DepartmentMembershipRevoked", tenant, "x", dept), token, http.StatusBadRequest, true},
		{"bad tenant id", http.MethodPost, eventsPath, envelope("DepartmentMembershipRevoked", "x", user, dept), token, http.StatusBadRequest, false},
		{"bad department id", http.MethodPost, eventsPath, envelope("DepartmentMembershipRevoked", tenant, user, "x"), token, http.StatusBadRequest, true},
		{"no token", http.MethodPost, eventsPath, envelope("MembershipRevoked", tenant, user, ""), "", http.StatusUnauthorized, true},
		{"oversized body", http.MethodPost, eventsPath, `{"data":"` + strings.Repeat("a", 11<<20) + `"}`, token, http.StatusRequestEntityTooLarge, false},
	})
	runExchanges(t, spec, failing, []exchange{
		{"processing failure", http.MethodPost, eventsPath, envelope("DepartmentMembershipRevoked", tenant, user, dept), token, http.StatusInternalServerError, true},
	})
}

func TestInternalContract_ConnectorAliases(t *testing.T) {
	spec := contract.Load(t, internalSpec)
	const token = "internal-secret"
	alias := `{"alias":"crm","method":"POST","baseURL":"http://crm.internal","pathTemplate":"/v1/x","timeoutMs":2500}`
	listed := ingestRouter(t, fakeMembership{}, fakeConnectors{aliases: []domain.ConnectorRestAlias{
		{Alias: "crm", Method: "POST", BaseURL: "http://crm.internal", PathTemplate: "/v1/x", Timeout: 2500 * time.Millisecond},
	}})
	invalid := ingestRouter(t, fakeMembership{}, fakeConnectors{writeErr: domain.ErrInvalidConnectorAliasInput})
	broken := ingestRouter(t, fakeMembership{}, fakeConnectors{listErr: errors.New("db down"), writeErr: errors.New("db down"), deleteErr: errors.New("db down")})
	missing := ingestRouter(t, fakeMembership{}, fakeConnectors{})
	found := ingestRouter(t, fakeMembership{}, fakeConnectors{deleteFound: true})
	restPath := aliasesPath + "/rest"

	runExchanges(t, spec, listed, []exchange{
		{"list", http.MethodGet, aliasesPath, "", token, http.StatusOK, true},
		{"list without token", http.MethodGet, aliasesPath, "", "", http.StatusUnauthorized, true},
		{"write", http.MethodPost, restPath, alias, token, http.StatusNoContent, true},
		{"write with a missing field", http.MethodPost, restPath, `{"alias":"crm"}`, token, http.StatusBadRequest, false},
		{"write without token", http.MethodPost, restPath, alias, "", http.StatusUnauthorized, true},
		{"write with an oversized body", http.MethodPost, restPath, `{"alias":"` + strings.Repeat("a", 11<<20) + `"}`, token, http.StatusRequestEntityTooLarge, false},
	})
	runExchanges(t, spec, invalid, []exchange{
		{"write refused by validation", http.MethodPost, restPath, alias, token, http.StatusBadRequest, true},
	})
	runExchanges(t, spec, broken, []exchange{
		{"list fails", http.MethodGet, aliasesPath, "", token, http.StatusInternalServerError, true},
		{"write fails", http.MethodPost, restPath, alias, token, http.StatusInternalServerError, true},
		{"delete fails", http.MethodDelete, restPath + "/crm", "", token, http.StatusInternalServerError, true},
	})
	runExchanges(t, spec, found, []exchange{
		{"delete", http.MethodDelete, restPath + "/crm", "", token, http.StatusNoContent, true},
	})
	runExchanges(t, spec, missing, []exchange{
		{"delete unknown", http.MethodDelete, restPath + "/crm", "", token, http.StatusNotFound, true},
		{"delete without token", http.MethodDelete, restPath + "/crm", "", "", http.StatusUnauthorized, true},
	})
}
