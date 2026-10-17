package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

func TestCatalogClient_ReadsThePlanTableAsTheInternalSystemPrincipal(t *testing.T) {
	var gotPath string
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotHeaders = r.URL.Path, r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"plans":[
			{"code":"starter","workflow_template_limit":5,"tender_limit":3,"feature_set":{"x":1}},
			{"code":"pro","workflow_template_limit":50},
			{"code":"enterprise","workflow_template_limit":null},
			{"code":"frozen","workflow_template_limit":0}
		],"record_versions":{"starter":1}}`))
	}))
	defer srv.Close()

	table, err := NewCatalogClient(srv.URL, time.Second).ReadPlanTable(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "/api/v1/internal/plans", gotPath)
	assert.Equal(t, "iam-system", gotHeaders.Get("x-user-id"))
	assert.Equal(t, "iam-system", gotHeaders.Get("x-tenant-roles"))
	assert.Equal(t, "00000000-0000-0000-0000-000000000000", gotHeaders.Get("x-tenant-id"))
	require.Len(t, table, 4)
	assert.Equal(t, int64(5), *table["starter"])
	assert.Equal(t, int64(50), *table["pro"])
	assert.Nil(t, table["enterprise"], "a null limit is unlimited")
	require.Contains(t, table, "enterprise")
	assert.Equal(t, int64(0), *table["frozen"])
}

func TestCatalogClient_AFailureIsUpstreamUnavailable(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"server error": func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
		"forbidden":    func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) },
		"not json":     func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) },
		"no plans":     func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{"plans":[]}`)) },
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(handler)
			defer srv.Close()
			_, err := NewCatalogClient(srv.URL, time.Second).ReadPlanTable(context.Background())
			assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
		})
	}
}

func TestCatalogClient_AnUnreachableCatalogIsUpstreamUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close()
	_, err := NewCatalogClient(url, time.Second).ReadPlanTable(context.Background())
	assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
}

func TestCatalogClient_RecordsTheDependencyMetric(t *testing.T) {
	registerMetrics(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"plans":[{"code":"starter","workflow_template_limit":5}]}`))
	}))
	defer srv.Close()
	before := dependencySeries(t, "catalog", "read_plan_table", "success")

	_, err := NewCatalogClient(srv.URL, time.Second).ReadPlanTable(context.Background())
	require.NoError(t, err)
	assert.Equal(t, before+1, dependencySeries(t, "catalog", "read_plan_table", "success"))
}

func TestCatalogClient_CapsTheResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(oversizedJSON("plans")))
	}))
	defer srv.Close()
	_, err := NewCatalogClient(srv.URL, time.Second).ReadPlanTable(context.Background())
	assert.ErrorIs(t, err, domain.ErrUpstreamUnavailable)
}
