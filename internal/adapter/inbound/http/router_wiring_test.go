package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-connectors/v2/pkg/registry"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/core/domain"
)

const wiringInternalToken = "wiring-secret"

type wiringConnectorFake struct {
	mu        sync.Mutex
	aliases   []domain.ConnectorRestAlias
	written   []domain.ConnectorRestAlias
	listCalls int
	deadline  time.Time
	hasDL     bool
}

func (f *wiringConnectorFake) Registry(context.Context) map[string]registry.Definition { return nil }

func (f *wiringConnectorFake) WriteCredential(
	context.Context, uuid.UUID, uuid.UUID, string, string, string,
) (string, error) {
	return "", nil
}

func (f *wiringConnectorFake) RevokeCredential(
	context.Context, uuid.UUID, uuid.UUID, string, string,
) error {
	return nil
}

func (f *wiringConnectorFake) ListAliases(ctx context.Context) ([]domain.ConnectorRestAlias, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls++
	f.deadline, f.hasDL = ctx.Deadline()
	return f.aliases, nil
}

func (f *wiringConnectorFake) WriteRestAlias(_ context.Context, a domain.ConnectorRestAlias) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.written = append(f.written, a)
	return nil
}

func (f *wiringConnectorFake) DeleteRestAlias(context.Context, string) (bool, error) {
	return false, nil
}

func newWiringEngine(t *testing.T, appEnv string, cfgTimeout time.Duration, conn *wiringConnectorFake) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfg := testGinConfig
	cfg.RequestTimeout = cfgTimeout
	return NewRouter(RouterConfig{
		GinConfig:        cfg,
		AppEnv:           appEnv,
		InternalAPIToken: wiringInternalToken,
		Handler:          handler.New(handler.Services{Connectors: conn}),
		DB:               fakeDBPinger{health: DBHealth{Healthy: true}},
		Cache:            fakePinger{},
	}).engine
}

func TestInternalRoutes_NoIdentityHeadersAndPlainText_ReachTheHandler(t *testing.T) {
	cases := []struct {
		name        string
		contentType string
	}{
		{name: "plain text content type", contentType: "text/plain"},
		{name: "no content type", contentType: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &wiringConnectorFake{aliases: []domain.ConnectorRestAlias{{
				Alias: "billing", Method: "POST", BaseURL: "https://billing.example", PathTemplate: "/v1/{id}",
				Timeout: 3 * time.Second,
			}}}
			engine := newWiringEngine(t, "test", RequestTimeout, conn)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/internal/connector-aliases", nil)
			req.Header.Set("x-internal-token", wiringInternalToken)
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			assert.Contains(t, w.Body.String(), `"alias":"billing"`)
			assert.Equal(t, 1, conn.listCalls, "the handler must run without identity headers or a JSON content type")
		})
	}
}

func TestInternalRoutes_PlainTextBodyOnAliasWrite_IsDecidedByTheHandlerBind(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantStatus  int
		wantInBody  string
		wantWritten int
	}{
		{
			name: "not json is a bind failure", body: "alias=billing",
			wantStatus: http.StatusBadRequest, wantInBody: `"code":"BAD_REQUEST"`, wantWritten: 0,
		},
		{
			name:       "valid json is accepted despite the content type",
			body:       `{"alias":"billing","method":"POST","baseURL":"https://billing.example","pathTemplate":"/v1/x"}`,
			wantStatus: http.StatusNoContent, wantWritten: 1,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &wiringConnectorFake{}
			engine := newWiringEngine(t, "test", RequestTimeout, conn)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/connector-aliases/rest", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "text/plain")
			req.Header.Set("x-internal-token", wiringInternalToken)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			assert.NotEqual(t, http.StatusUnsupportedMediaType, w.Code)
			if tc.wantInBody != "" {
				assert.Contains(t, w.Body.String(), tc.wantInBody)
			}
			require.Len(t, conn.written, tc.wantWritten)
			if tc.wantWritten == 1 {
				assert.Equal(t, "billing", conn.written[0].Alias)
				assert.Equal(t, "/v1/x", conn.written[0].PathTemplate)
			}
		})
	}
}

func TestNewRouter_ConfiguredRequestTimeout_IsOverwrittenWithThePackageTimeout(t *testing.T) {
	cases := []struct {
		name       string
		cfgTimeout time.Duration
	}{
		{name: "shorter than the package timeout", cfgTimeout: time.Millisecond},
		{name: "unset", cfgTimeout: 0},
		{name: "longer than the package timeout", cfgTimeout: time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn := &wiringConnectorFake{}
			engine := newWiringEngine(t, "test", tc.cfgTimeout, conn)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/internal/connector-aliases", nil)
			req.Header.Set("x-internal-token", wiringInternalToken)
			w := httptest.NewRecorder()
			engine.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			require.True(t, conn.hasDL, "the request context must carry a deadline")
			remaining := time.Until(conn.deadline)
			assert.Greater(t, remaining, RequestTimeout/2, "the configured value must not shorten the deadline")
			assert.LessOrEqual(t, remaining, RequestTimeout, "the configured value must not lengthen the deadline")
		})
	}
}

func TestNewRouter_DevMode_ServesTheSpecFilesFromDisk(t *testing.T) {
	cases := []struct {
		name     string
		path     string
		file     string
		inRepo   bool
		wantCode int
	}{
		{name: "public spec", path: "/api/openapi.yaml", file: "docs/swagger/openapi.yaml", inRepo: true, wantCode: http.StatusOK},
		{name: "internal spec", path: "/api/internal-openapi.yaml", file: "api/internal-openapi.yaml", inRepo: true, wantCode: http.StatusOK},
		{name: "public spec missing", path: "/api/openapi.yaml", file: "docs/swagger/openapi.yaml", inRepo: false, wantCode: http.StatusNotFound},
		{name: "internal spec missing", path: "/api/internal-openapi.yaml", file: "api/internal-openapi.yaml", inRepo: false, wantCode: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var want []byte
			if tc.inRepo {
				t.Chdir("../../../..")
				var err error
				want, err = os.ReadFile(tc.file)
				require.NoError(t, err)
			} else {
				t.Chdir(t.TempDir())
			}
			engine := newWiringEngine(t, "dev", RequestTimeout, &wiringConnectorFake{})

			w := httptest.NewRecorder()
			engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))

			require.Equal(t, tc.wantCode, w.Code)
			if tc.inRepo {
				assert.Equal(t, string(want), w.Body.String(), "the response is the file on disk, with no identity headers sent")
			} else {
				assert.NotContains(t, w.Body.String(), "openapi:")
			}
		})
	}
}
