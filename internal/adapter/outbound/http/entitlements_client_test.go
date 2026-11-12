package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

var entitlementsTenant = uuid.MustParse("11111111-1111-1111-1111-111111111111")

// answer wraps a limits object in a valid answer for entitlementsTenant.
func answer(limits string) string {
	return `{"tenant_id":"` + entitlementsTenant.String() + `",` + limits + `}`
}

const limitFive = `"limits":{"workflow_template_limit":{"value":5}}`

func entitlementsServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestEntitlementsClient_ReadsTheLimitAsTheSystemPrincipalForTheTenant(t *testing.T) {
	var gotPath, gotMethod string
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotHeaders = r.URL.Path, r.Method, r.Header.Clone()
		_, _ = w.Write([]byte(`{"tenant_id":"` + entitlementsTenant.String() + `","limits":{"workflow_template_limit":{"value":80,"source":"override"}}}`))
	}))
	defer srv.Close()

	got, err := NewEntitlementsClient(srv.URL, time.Second).ReadWorkflowTemplateLimit(context.Background(), entitlementsTenant)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(80), *got)
	assert.Equal(t, http.MethodGet, gotMethod)
	assert.Equal(t, "/api/v1/internal/tenants/"+entitlementsTenant.String()+"/entitlements", gotPath)
	assert.Equal(t, "iam-system", gotHeaders.Get("x-user-id"))
	assert.Equal(t, "iam-system", gotHeaders.Get("x-tenant-roles"))
	assert.Equal(t, entitlementsTenant.String(), gotHeaders.Get("x-tenant-id"))
}

func TestEntitlementsClient_AcceptedBodies(t *testing.T) {
	for name, tc := range map[string]struct {
		body string
		want *int64
	}{
		"a plain value":               {answer(`"limits":{"workflow_template_limit":{"value":5,"source":"plan"}}`), limitPtr(5)},
		"zero is a real limit":        {answer(`"limits":{"workflow_template_limit":{"value":0}}`), limitPtr(0)},
		"null is unlimited":           {answer(`"limits":{"workflow_template_limit":{"value":null,"source":"plan"}}`), nil},
		"other limits are ignored":    {answer(`"limits":{"tender_limit":{"value":3},"workflow_template_limit":{"value":9}},"features":{"x":true}`), limitPtr(9)},
		"trailing whitespace is fine": {answer(limitFive) + " \n\t\n", limitPtr(5)},
	} {
		t.Run(name, func(t *testing.T) {
			srv := entitlementsServer(t, http.StatusOK, tc.body)

			got, err := NewEntitlementsClient(srv.URL, time.Second).ReadWorkflowTemplateLimit(context.Background(), entitlementsTenant)

			require.NoError(t, err)
			if tc.want == nil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, *tc.want, *got)
		})
	}
}

// Each status refusal carries a valid body, so only the status check can refuse it.
func TestEntitlementsClient_AnyStatusButOKIsRefusedEvenWithAValidBody(t *testing.T) {
	for name, status := range map[string]int{
		"accepted":     http.StatusAccepted,
		"no content":   http.StatusNoContent,
		"forbidden":    http.StatusForbidden,
		"not found":    http.StatusNotFound,
		"server error": http.StatusInternalServerError,
		"unavailable":  http.StatusServiceUnavailable,
	} {
		t.Run(name, func(t *testing.T) {
			srv := entitlementsServer(t, status, answer(limitFive))

			got, err := NewEntitlementsClient(srv.URL, time.Second).ReadWorkflowTemplateLimit(context.Background(), entitlementsTenant)

			require.Error(t, err)
			assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
			assert.ErrorContains(t, err, strconv.Itoa(status))
			assert.Nil(t, got)
		})
	}
}

func TestEntitlementsClient_RefusedBodiesAreUpstreamUnavailable(t *testing.T) {
	other := uuid.MustParse("22222222-2222-2222-2222-222222222222").String()
	for name, body := range map[string]string{
		"not json":                       `<html>`,
		"empty body":                     ``,
		"a json array":                   `[]`,
		"limits absent":                  answer(`"x":1`),
		"limits null":                    answer(`"limits":null`),
		"the limit absent":               answer(`"limits":{"tender_limit":{"value":3}}`),
		"the limit null":                 answer(`"limits":{"workflow_template_limit":null}`),
		"value absent":                   answer(`"limits":{"workflow_template_limit":{"source":"plan"}}`),
		"value absent in an empty limit": answer(`"limits":{"workflow_template_limit":{}}`),
		"negative value":                 answer(`"limits":{"workflow_template_limit":{"value":-1}}`),
		"integer written with a point":   answer(`"limits":{"workflow_template_limit":{"value":12.0}}`),
		"fractional value":               answer(`"limits":{"workflow_template_limit":{"value":1.5}}`),
		"string value":                   answer(`"limits":{"workflow_template_limit":{"value":"80"}}`),
		"boolean value":                  answer(`"limits":{"workflow_template_limit":{"value":true}}`),
		"value too large":                answer(`"limits":{"workflow_template_limit":{"value":99999999999999999999}}`),
		"another tenant's set":           `{"tenant_id":"` + other + `",` + limitFive + `}`,
		"an unreadable tenant id":        `{"tenant_id":"nope",` + limitFive + `}`,
		"an empty tenant id":             `{"tenant_id":"",` + limitFive + `}`,
		"a tenant id of the wrong type":  `{"tenant_id":7,` + limitFive + `}`,
		"a second json value after it":   answer(limitFive) + `{"tenant_id":"` + other + `"}`,
		"garbage after it":               answer(limitFive) + ` x`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := entitlementsServer(t, http.StatusOK, body)

			got, err := NewEntitlementsClient(srv.URL, time.Second).ReadWorkflowTemplateLimit(context.Background(), entitlementsTenant)

			require.Error(t, err)
			assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
			assert.Nil(t, got)
		})
	}
}

func TestEntitlementsClient_AnAnswerWithoutATenantIDIsRefusedAndANullValueStaysUnlimited(t *testing.T) {
	for name, body := range map[string]string{
		"absent": `{` + limitFive + `}`,
		"null":   `{"tenant_id":null,` + limitFive + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := entitlementsServer(t, http.StatusOK, body)

			got, err := NewEntitlementsClient(srv.URL, time.Second).ReadWorkflowTemplateLimit(context.Background(), entitlementsTenant)

			require.Error(t, err)
			assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
			assert.ErrorContains(t, err, "tenant_id")
			assert.Nil(t, got)
		})
	}
	srv := entitlementsServer(t, http.StatusOK, answer(`"limits":{"workflow_template_limit":{"value":null}}`))

	got, err := NewEntitlementsClient(srv.URL, time.Second).ReadWorkflowTemplateLimit(context.Background(), entitlementsTenant)

	require.NoError(t, err)
	assert.Nil(t, got, "a null value with the right tenant is still unlimited")
}

func TestEntitlementsClient_AnErrorStatusCarriesASnippetOfTheBody(t *testing.T) {
	srv := entitlementsServer(t, http.StatusBadGateway, "gateway said no")

	_, err := NewEntitlementsClient(srv.URL, time.Second).ReadWorkflowTemplateLimit(context.Background(), entitlementsTenant)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "502")
	assert.Contains(t, err.Error(), "gateway said no")
}

func TestEntitlementsClient_ATrailingSlashOnTheBaseURLIsIgnored(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(answer(`"limits":{"workflow_template_limit":{"value":1}}`)))
	}))
	defer srv.Close()

	_, err := NewEntitlementsClient(srv.URL+"//", time.Second).ReadWorkflowTemplateLimit(context.Background(), entitlementsTenant)

	require.NoError(t, err)
	assert.Equal(t, "/api/v1/internal/tenants/"+entitlementsTenant.String()+"/entitlements", gotPath)
}

func TestEntitlementsClient_ATransportFailureIsUpstreamUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close()

	_, err := NewEntitlementsClient(addr, time.Second).ReadWorkflowTemplateLimit(context.Background(), entitlementsTenant)

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
}

func TestEntitlementsClient_ATimeoutIsUpstreamUnavailable(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) { <-release }))
	defer srv.Close()
	defer close(release)

	_, err := NewEntitlementsClient(srv.URL, 50*time.Millisecond).ReadWorkflowTemplateLimit(context.Background(), entitlementsTenant)

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
}

// sizedAnswer pads a valid answer with trailing spaces to exactly size bytes.
func sizedAnswer(t *testing.T, size int) string {
	t.Helper()
	body := answer(limitFive)
	require.LessOrEqual(t, len(body), size)
	return body + strings.Repeat(" ", size-len(body))
}

func TestEntitlementsClient_ABodyOfExactlyTheCapIsAccepted(t *testing.T) {
	srv := entitlementsServer(t, http.StatusOK, sizedAnswer(t, maxResponseBody))

	got, err := NewEntitlementsClient(srv.URL, 5*time.Second).ReadWorkflowTemplateLimit(context.Background(), entitlementsTenant)

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(5), *got)
}

func TestEntitlementsClient_ABodyOneByteOverTheCapIsReportedAsTooLargeNotAsATruncatedDocument(t *testing.T) {
	srv := entitlementsServer(t, http.StatusOK, sizedAnswer(t, maxResponseBody+1))

	got, err := NewEntitlementsClient(srv.URL, 5*time.Second).ReadWorkflowTemplateLimit(context.Background(), entitlementsTenant)

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
	assert.ErrorContains(t, err, "larger than")
	assert.NotContains(t, err.Error(), "unexpected EOF")
	assert.Nil(t, got)
}

func TestEntitlementsClient_ADocumentCutByTheCapIsReportedAsTooLarge(t *testing.T) {
	big := entitlementsServer(t, http.StatusOK, `{"pad":"`+strings.Repeat("a", maxResponseBody)+`",`+limitFive+`}`)

	_, err := NewEntitlementsClient(big.URL, 5*time.Second).ReadWorkflowTemplateLimit(context.Background(), entitlementsTenant)

	require.Error(t, err)
	assert.ErrorContains(t, err, "larger than")
	assert.NotContains(t, err.Error(), "unexpected EOF")
}

func TestEntitlementsClient_ARequestThatCannotBeBuiltIsUpstreamUnavailable(t *testing.T) {
	_, err := NewEntitlementsClient("http://bad host", time.Second).ReadWorkflowTemplateLimit(context.Background(), entitlementsTenant)

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
}

func TestEntitlementsClient_ACancelledContextIsUpstreamUnavailable(t *testing.T) {
	srv := entitlementsServer(t, http.StatusOK, answer(limitFive))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := NewEntitlementsClient(srv.URL, time.Second).ReadWorkflowTemplateLimit(ctx, entitlementsTenant)

	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
}

func limitPtr(n int64) *int64 { return &n }
