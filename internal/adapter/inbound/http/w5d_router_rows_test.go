package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/workflow-definition-service/internal/adapter/inbound/http/handler"
)

type w5dCountingDB struct {
	health DBHealth
	calls  atomic.Int32
}

func (d *w5dCountingDB) Health(context.Context) DBHealth {
	d.calls.Add(1)
	return d.health
}

type w5dCountingCache struct{ calls atomic.Int32 }

func (c *w5dCountingCache) Ping(context.Context) error {
	c.calls.Add(1)
	return nil
}

type w5dProblem struct {
	Type     string `json:"type"`
	Title    string `json:"title"`
	Status   int    `json:"status"`
	Detail   string `json:"detail"`
	Instance string `json:"instance"`
	Code     string `json:"code"`
}

func w5dRouter(appEnv string, db DBPinger, cache Pinger) *Router {
	gin.SetMode(gin.TestMode)
	return NewRouter(RouterConfig{
		GinConfig: testGinConfig,
		AppEnv:    appEnv,
		Handler:   handler.New(handler.Services{}),
		DB:        db,
		Cache:     cache,
	})
}

func w5dDecodeProblem(t *testing.T, w *httptest.ResponseRecorder) w5dProblem {
	t.Helper()
	require.Equal(t, "application/problem+json", w.Header().Get("Content-Type"), w.Body.String())
	var p w5dProblem
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &p), w.Body.String())
	return p
}

func w5dDecodeMap(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &m), w.Body.String())
	return m
}

func w5dGet(engine http.Handler, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

func w5dIdentity(req *http.Request) {
	req.Header.Set("x-tenant-id", uuid.NewString())
	req.Header.Set("x-user-id", uuid.NewString())
	req.Header.Set("x-tenant-roles", "tenant_admin")
}

func TestNewRouter_AnyEnvironmentButDev_RegistersNoDocumentationRoute(t *testing.T) {
	t.Parallel()

	envs := []string{"local", "test", "staging", "prod", "production", ""}
	paths := []string{
		"/swagger/index.html",
		"/swagger/index.css",
		"/swagger/swagger-initializer.js",
		"/asyncapi",
		"/api/openapi.yaml",
		"/api/internal-openapi.yaml",
	}
	for _, env := range envs {
		t.Run("env="+env, func(t *testing.T) {
			t.Parallel()
			engine := w5dRouter(env, fakeDBPinger{}, fakePinger{}).engine
			for _, path := range paths {
				t.Run(path, func(t *testing.T) {
					t.Parallel()
					w := w5dGet(engine, path)

					require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
					p := w5dDecodeProblem(t, w)
					assert.Equal(t, "NOT_FOUND", p.Code)
					assert.Equal(t, path, p.Instance)
				})
			}
		})
	}
}

func TestNewRouter_Environment_DecidesTheGinMode(t *testing.T) {
	t.Cleanup(func() { gin.SetMode(gin.TestMode) })

	cases := []struct {
		name     string
		env      string
		wantMode string
	}{
		{name: "dev leaves the mode as it was", env: "dev", wantMode: gin.TestMode},
		{name: "local switches to release", env: "local", wantMode: gin.ReleaseMode},
		{name: "test switches to release", env: "test", wantMode: gin.ReleaseMode},
		{name: "staging switches to release", env: "staging", wantMode: gin.ReleaseMode},
		{name: "prod switches to release", env: "prod", wantMode: gin.ReleaseMode},
		{name: "production switches to release", env: "production", wantMode: gin.ReleaseMode},
		{name: "an empty environment switches to release", env: "", wantMode: gin.ReleaseMode},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)

			NewRouter(RouterConfig{
				GinConfig: testGinConfig,
				AppEnv:    tc.env,
				Handler:   handler.New(handler.Services{}),
				DB:        fakeDBPinger{},
				Cache:     fakePinger{},
			})

			assert.Equal(t, tc.wantMode, gin.Mode())
		})
	}
}

func TestSwaggerRoute_DevMode_ThemeAndInitializerAreServedUncachedFromTheEmbeddedFiles(t *testing.T) {
	t.Parallel()

	engine := w5dRouter("dev", fakeDBPinger{}, fakePinger{}).engine
	cases := []struct {
		name        string
		target      string
		file        string
		contentType string
	}{
		{"theme", "/swagger/index.css", "static/swagger-theme.css", "text/css; charset=utf-8"},
		{"theme with a query string", "/swagger/index.css?v=2", "static/swagger-theme.css", "text/css; charset=utf-8"},
		{"initializer", "/swagger/swagger-initializer.js", "static/swagger-initializer.js", "application/javascript; charset=utf-8"},
		{"initializer with a query string", "/swagger/swagger-initializer.js?v=2", "static/swagger-initializer.js", "application/javascript; charset=utf-8"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want, err := os.ReadFile(tc.file)
			require.NoError(t, err)

			w := w5dGet(engine, tc.target)

			require.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, tc.contentType, w.Header().Get("Content-Type"))
			assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"))
			assert.Equal(t, string(want), w.Body.String())
		})
	}
}

func TestReadyz_ReadersRacingTheDrain_EveryRequestStartedAfterItIsDraining(t *testing.T) {
	const workers = 16
	const afterDrainRequests = 20

	router := w5dRouter("test", &w5dCountingDB{health: DBHealth{Healthy: true}}, &w5dCountingCache{})

	type result struct {
		afterDrain bool
		code       int
		body       string
	}
	var (
		mu         sync.Mutex
		results    []result
		drained    = make(chan struct{})
		firstDone  sync.WaitGroup
		allStopped sync.WaitGroup
	)
	firstDone.Add(workers)
	allStopped.Add(workers)
	for range workers {
		go func() {
			defer allStopped.Done()
			var local []result
			record := func(after bool) {
				w := httptest.NewRecorder()
				router.engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
				local = append(local, result{afterDrain: after, code: w.Code, body: w.Body.String()})
			}
			record(false)
			firstDone.Done()
			for {
				select {
				case <-drained:
					for range afterDrainRequests {
						record(true)
					}
					mu.Lock()
					results = append(results, local...)
					mu.Unlock()
					return
				default:
					record(false)
				}
			}
		}()
	}

	firstDone.Wait()
	router.BeginDrain()
	close(drained)
	allStopped.Wait()

	after := 0
	for _, r := range results {
		if !r.afterDrain {
			assert.Contains(t, []int{http.StatusOK, http.StatusServiceUnavailable}, r.code)
			continue
		}
		after++
		assert.Equal(t, http.StatusServiceUnavailable, r.code)
		assert.JSONEq(t, `{"status":"draining"}`, r.body)
	}
	assert.Equal(t, workers*afterDrainRequests, after)
}

func TestAsyncAPIRoute_SpecUnavailable_Returns500ProblemThatHidesTheCause(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
	}{
		{name: "the api folder does not exist", setup: func(*testing.T, string) {}},
		{
			name: "the spec path is a directory",
			setup: func(t *testing.T, dir string) {
				require.NoError(t, os.MkdirAll(filepath.Join(dir, "api", "asyncapi.yaml"), 0o755))
			},
		},
		{
			name: "the spec is not valid yaml",
			setup: func(t *testing.T, dir string) {
				require.NoError(t, os.MkdirAll(filepath.Join(dir, "api"), 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(dir, "api", "asyncapi.yaml"), []byte("a: [\n"), 0o600))
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.setup(t, dir)
			t.Chdir(dir)
			engine := w5dRouter("dev", fakeDBPinger{}, fakePinger{}).engine

			w := w5dGet(engine, "/asyncapi")

			require.Equal(t, http.StatusInternalServerError, w.Code)
			p := w5dDecodeProblem(t, w)
			assert.Equal(t, "INTERNAL_ERROR", p.Code)
			assert.Equal(t, "https://api.workflow.platform/errors/internal-error", p.Type)
			assert.Equal(t, http.StatusInternalServerError, p.Status)
			assert.Equal(t, "the AsyncAPI document is unavailable", p.Detail)
			assert.Equal(t, "/asyncapi", p.Instance)
			assert.NotContains(t, w.Body.String(), "asyncapi.yaml")
			assert.NotContains(t, w.Body.String(), dir)
		})
	}
}

func TestInternalRoutes_WithoutTheSystemCallerIdentity_IsRefusedAsUnauthorized(t *testing.T) {
	t.Parallel()

	engine := w5dRouter("test", fakeDBPinger{}, fakePinger{}).engine
	body := `{"id":"x","type":"Unhandled","data":{"pad":"small"}}`

	cases := []struct {
		name    string
		headers map[string]string
	}{
		{name: "an ordinary user role", headers: map[string]string{"x-user-id": "u", "x-tenant-roles": "tenant_admin"}},
		{name: "the system role without a user id", headers: map[string]string{"x-tenant-roles": "iam-system"}},
		{name: "a user id without roles", headers: map[string]string{"x-user-id": "iam-system"}},
		{name: "the retired token header alone", headers: map[string]string{"x-internal-token": "secret"}},
		{name: "no headers"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/events", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()

			engine.ServeHTTP(w, req)

			require.Equal(t, http.StatusUnauthorized, w.Code)
			p := w5dDecodeProblem(t, w)
			assert.Equal(t, "UNAUTHORIZED", p.Code)
			assert.Equal(t, "https://api.workflow.platform/errors/unauthorized", p.Type)
			assert.Equal(t, "missing or invalid internal caller identity", p.Detail)
			assert.Equal(t, "/api/v1/internal/events", p.Instance)
		})
	}
}

func TestPublicRoutes_NonJSONContentType_IsRefusedAsUnsupportedMediaType(t *testing.T) {
	t.Parallel()

	engine := w5dRouter("test", fakeDBPinger{}, fakePinger{}).engine
	body := "plain text body"

	cases := []struct {
		name        string
		contentType string
	}{
		{name: "text/plain", contentType: "text/plain"},
		{name: "text/plain with a charset", contentType: "text/plain; charset=utf-8"},
		{name: "no content type", contentType: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader(body))
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			w5dIdentity(req)
			w := httptest.NewRecorder()

			engine.ServeHTTP(w, req)

			require.Equal(t, http.StatusUnsupportedMediaType, w.Code, w.Body.String())
			p := w5dDecodeProblem(t, w)
			assert.Equal(t, "UNSUPPORTED_MEDIA_TYPE", p.Code)
			assert.Equal(t, "https://api.workflow.platform/errors/unsupported-media-type", p.Type)
			assert.Equal(t, "Content-Type must be application/json", p.Detail)
			assert.Equal(t, "/api/v1/workflows", p.Instance)
		})
	}
}

func TestPublicRoutes_MalformedIdentityWithPlainTextBody_IsRefusedAsUnauthorizedBeforeTheContentType(t *testing.T) {
	t.Parallel()

	engine := w5dRouter("test", fakeDBPinger{}, fakePinger{}).engine

	cases := []struct {
		name       string
		tenant     string
		user       string
		wantStatus int
		wantCode   string
	}{
		{name: "tenant is not a uuid", tenant: "not-a-uuid", user: uuid.NewString(), wantStatus: http.StatusUnauthorized, wantCode: "UNAUTHORIZED"},
		{name: "user is not a uuid", tenant: uuid.NewString(), user: "not-a-uuid", wantStatus: http.StatusUnauthorized, wantCode: "UNAUTHORIZED"},
		{name: "the same request with a valid identity reaches the content type check", tenant: uuid.NewString(), user: uuid.NewString(), wantStatus: http.StatusUnsupportedMediaType, wantCode: "UNSUPPORTED_MEDIA_TYPE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/workflows", strings.NewReader("plain text body"))
			req.Header.Set("Content-Type", "text/plain")
			req.Header.Set("x-tenant-id", tc.tenant)
			req.Header.Set("x-user-id", tc.user)
			req.Header.Set("x-tenant-roles", "tenant_admin")
			w := httptest.NewRecorder()

			engine.ServeHTTP(w, req)

			require.Equal(t, tc.wantStatus, w.Code, w.Body.String())
			p := w5dDecodeProblem(t, w)
			assert.Equal(t, tc.wantCode, p.Code)
			assert.Equal(t, tc.wantStatus, p.Status)
		})
	}
}

func TestReadyz_HealthyDatabase_PingsTheCacheOnceAndReportsThePoolNumbers(t *testing.T) {
	t.Parallel()

	db := &w5dCountingDB{health: DBHealth{Healthy: true, Utilization: 0.75, AcquiredConns: 15, MaxConns: 20}}
	cache := &w5dCountingCache{}
	router := w5dRouter("test", db, cache)

	w := w5dGet(router.engine, "/readyz")

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, map[string]any{
		"status":         "OK",
		"cache":          "ok",
		"db_utilization": 0.75,
		"db_conns":       float64(15),
		"db_max_conns":   float64(20),
	}, w5dDecodeMap(t, w))
	assert.Equal(t, int32(1), db.calls.Load())
	assert.Equal(t, int32(1), cache.calls.Load())
}

func TestReadyz_UnhealthyDatabase_ReportsUnreachableWithoutPingingTheCache(t *testing.T) {
	t.Parallel()

	db := &w5dCountingDB{health: DBHealth{Healthy: false}}
	cache := &w5dCountingCache{}
	router := w5dRouter("test", db, cache)

	w := w5dGet(router.engine, "/readyz")

	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Equal(t, map[string]any{"status": "unavailable", "db": "unreachable"}, w5dDecodeMap(t, w))
	assert.Equal(t, int32(1), db.calls.Load())
	assert.Zero(t, cache.calls.Load(), "the cache is only asked once the database is known to be healthy")
}

func TestReadyz_AfterBeginDrain_ReportsDrainingWithoutAskingTheDatabaseOrTheCache(t *testing.T) {
	t.Parallel()

	db := &w5dCountingDB{health: DBHealth{Healthy: true, Utilization: 0.5, AcquiredConns: 1, MaxConns: 2}}
	cache := &w5dCountingCache{}
	router := w5dRouter("test", db, cache)

	before := w5dGet(router.engine, "/readyz")
	require.Equal(t, http.StatusOK, before.Code)
	require.Equal(t, int32(1), db.calls.Load())
	require.Equal(t, int32(1), cache.calls.Load())

	router.BeginDrain()
	after := w5dGet(router.engine, "/readyz")

	require.Equal(t, http.StatusServiceUnavailable, after.Code)
	assert.Equal(t, map[string]any{"status": "draining"}, w5dDecodeMap(t, after))
	assert.Equal(t, int32(1), db.calls.Load(), "the drain answer must not ask the database")
	assert.Equal(t, int32(1), cache.calls.Load(), "the drain answer must not ask the cache")
}

func TestHealthz_DatabaseDownAndOrDraining_StillAnswersOkWithoutAskingAnyone(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		drain bool
	}{
		{name: "database down"},
		{name: "database down while draining", drain: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := &w5dCountingDB{health: DBHealth{Healthy: false}}
			cache := &w5dCountingCache{}
			router := w5dRouter("test", db, cache)
			if tc.drain {
				router.BeginDrain()
			}
			readyz := w5dGet(router.engine, "/readyz")
			require.Equal(t, http.StatusServiceUnavailable, readyz.Code, "the setup must make the pod not ready")
			callsBefore := db.calls.Load()

			w := w5dGet(router.engine, "/healthz")

			require.Equal(t, http.StatusOK, w.Code)
			assert.JSONEq(t, `{"status":"ok"}`, w.Body.String())
			assert.Equal(t, callsBefore, db.calls.Load())
			assert.Zero(t, cache.calls.Load())
		})
	}
}
