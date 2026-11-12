package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/observability"
)

func TestEntitlementsClient_UsesItsOwnTransportWith32IdleConnectionsPerHostAndKeepsTheOtherClientsOnTheDefault(t *testing.T) {
	traced, ok := NewEntitlementsClient("http://entitlements", time.Second).client.Transport.(traceTransport)
	require.True(t, ok, "the trace wrapper stays")
	transport, ok := traced.base.(*http.Transport)

	require.True(t, ok, "the client carries its own *http.Transport")
	assert.Equal(t, 32, transport.MaxIdleConnsPerHost)
	assert.NotSame(t, http.DefaultTransport, transport, "a clone, so the process default is untouched")
	assert.Zero(t, http.DefaultTransport.(*http.Transport).MaxIdleConnsPerHost)
	assert.Same(t, http.DefaultTransport, NewCatalogClient("http://catalog", time.Second).client.Transport.(traceTransport).base)
}

func TestEntitlementsClient_StillRefusesToFollowARedirect(t *testing.T) {
	client := NewEntitlementsClient("http://entitlements", time.Second).client

	require.NotNil(t, client.CheckRedirect)
	assert.ErrorIs(t, client.CheckRedirect(nil, nil), http.ErrUseLastResponse)
}

func TestEntitlementsClient_CountsAFailedReadByReason(t *testing.T) {
	registerMetrics(t)
	good := entitlementsServer(t, http.StatusOK, answer(limitFive))
	status := entitlementsServer(t, http.StatusBadGateway, ``)
	garbled := entitlementsServer(t, http.StatusOK, `<html>`)
	wrongTenant := entitlementsServer(t, http.StatusOK, `{"tenant_id":"22222222-2222-2222-2222-222222222222",`+limitFive+`}`)
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()
	count := func(reason string) float64 {
		return testutil.ToFloat64(observability.EntitlementsCallErrorsTotal.WithLabelValues(reason))
	}
	for name, tc := range map[string]struct{ url, reason string }{
		"a good answer counts nothing": {good.URL, ""},
		"a bad status":                 {status.URL, "status"},
		"an undecodable body":          {garbled.URL, "response"},
		"another tenant's answer":      {wrongTenant.URL, "response"},
		"a refused connection":         {closedURL, "transport"},
	} {
		t.Run(name, func(t *testing.T) {
			before := map[string]float64{"transport": count("transport"), "status": count("status"), "response": count("response")}

			_, _ = NewEntitlementsClient(tc.url, time.Second).ReadWorkflowTemplateLimit(context.Background(), entitlementsTenant)

			for reason, was := range before {
				want := was
				if reason == tc.reason {
					want++
				}
				assert.Equal(t, want, count(reason), reason)
			}
		})
	}
}

func TestEntitlementsClient_TheCallMetricStillRecordsTheDependency(t *testing.T) {
	registerMetrics(t)
	srv := entitlementsServer(t, http.StatusOK, answer(limitFive))
	before := dependencySeries(t, "entitlements", "read_limit", "success")

	_, err := NewEntitlementsClient(srv.URL, time.Second).ReadWorkflowTemplateLimit(context.Background(), entitlementsTenant)

	require.NoError(t, err)
	assert.Equal(t, before+1, dependencySeries(t, "entitlements", "read_limit", "success"))
}
